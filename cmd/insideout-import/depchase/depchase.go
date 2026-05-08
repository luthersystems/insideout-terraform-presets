package depchase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// DefaultMaxIterations bounds the depchase loop. Five matches
// driftfix's bound — the realistic case converges in one or two
// passes (operator's stack references a handful of external IAM/KMS
// resources; pulling them in produces a stack that's already
// internally consistent). The bound exists to surface unresolvable
// configurations as a fatal rather than spinning indefinitely on a
// reference graph that won't close.
const DefaultMaxIterations = 5

// ErrCyclicDependency signals that the same set of unresolved
// references surfaced across two successive iterations after the loop
// added new resources — i.e. the additions themselves did not change
// what's unresolved. In a clean stack this never happens; in a
// pathological one it points at a reference cycle the loop cannot
// resolve by adding more resources.
var ErrCyclicDependency = errors.New("depchase: unresolved set stable across iterations (cycle or reference target unreachable via DiscoverByID)")

// ErrMaxIterations signals the bound was hit before the unresolved
// set drained. The operator-facing message should include the
// remaining unresolved literals so they can be inspected manually.
var ErrMaxIterations = errors.New("depchase: max iterations exceeded")

// Discoverer is the per-ID discovery surface depchase needs from the
// awsdiscover package. The aggregator's DiscoverByID dispatches to
// the registered per-type discoverer; tests inject a fake.
type Discoverer interface {
	DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error)
}

// PipelineFns are the genconfig + driftfix re-runs depchase calls on
// each iteration's expanded resource set. The orchestrator passes the
// production wrappers; tests pass fakes that touch a synthetic
// generated.tf without standing up terraform.
//
// Contract: RunGenconfig must return a GenconfigResult whose
// Resources slice is a *superset* of the input — i.e. it may
// populate Attributes on the input resources or add metadata, but it
// must not drop entries. Depchase relies on this to keep the
// resolved-set monotonic across iterations; a regenerate that
// silently filters resources would let the loop oscillate by
// re-marking previously resolved ARNs as unresolved.
type PipelineFns struct {
	// RunGenconfig regenerates generated.tf from the current resource
	// set. Receives the current []ImportedResource (the original set
	// plus everything depchase has added so far) and is expected to
	// produce the same Workdir+generated.tf shape genconfig.Run would
	// emit. Returns a Result so the orchestrator can rewrite the
	// outer manifest with attribute-populated resources. Per the
	// PipelineFns contract above, the returned Resources slice must
	// include every input resource.
	RunGenconfig func(ctx context.Context, resources []imported.ImportedResource) (*GenconfigResult, error)
	// RunDriftfix runs the drift-fix loop against the regenerated
	// stack. Receives no input — all state lives in Workdir. Returns
	// the Iterations count for observability only.
	RunDriftfix func(ctx context.Context) (*DriftfixResult, error)
}

// GenconfigResult is the depchase-facing subset of genconfig.Result.
// Defined here so depchase doesn't import genconfig (which would form
// a cycle: discover.go imports depchase; depchase would import
// genconfig; genconfig is also imported by discover.go directly).
type GenconfigResult struct {
	GeneratedPath string
	Resources     []imported.ImportedResource
}

// DriftfixResult is the depchase-facing subset of driftfix.Result.
type DriftfixResult struct {
	GeneratedPath string
	Iterations    int
}

// Options is the input to Run. Workdir is the same scratch directory
// genconfig and driftfix share; depchase reads generated.tf from
// there and uses Pipeline to regenerate it on each iteration.
type Options struct {
	Workdir       string
	Region        string
	AccountID     string
	MaxIterations int
	Discoverer    Discoverer
	Pipeline      PipelineFns
}

// Result is what Run hands back. Resources is the final, expanded
// set (input + everything pulled in across all iterations).
// Iterations counts how many times the loop ran the regenerate +
// re-driftfix cycle (0 means "no unresolved refs on the original
// stack — nothing to do"). Warnings lists unresolvable / unsupported
// references the loop chose to surface rather than fail on. Edges
// records the dependency graph of each successful add (#297) so the
// CLI can persist it as graph.json next to imported.json.
type Result struct {
	GeneratedPath string
	Iterations    int
	Resources     []imported.ImportedResource
	Added         []imported.ImportedResource
	Warnings      []string

	// Edges is the dependency graph the loop built during chase: one
	// entry per (consumer → producer) Terraform-address pair where the
	// consumer's HCL referenced an ARN literal pointing at a resource
	// the loop pulled in via DiscoverByID. The picker uses Edges to
	// close the auto-include loop in the wizard UI: when the operator
	// selects a row, the wizard auto-includes every transitive
	// `dependsOn` target. The CLI persists this slice as graph.json
	// (#297). Empty when nothing was added; nil-safe (writeGraphManifest
	// substitutes []GraphEdge{} so the on-disk file is `[]`, never
	// `null`).
	Edges []GraphEdge
}

// GraphEdge is a single (from, to) Terraform-address pair representing
// "the resource at `from` references the resource at `to`." Addresses
// are used (rather than ImportIDs) because addresses are the canonical
// identifier the composer uses when wiring HCL in the generated stack;
// ImportIDs are not always stable across providers (e.g. AWS IAM uses
// the role name; GCP IAM uses the project + member tuple). The reliable
// wizard's picker reads (from, to) addresses verbatim into its
// dependsOn graph.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Run is the Stage 2c3 dependency-chase loop:
//
//  1. Read the cleaned generated.tf from Workdir.
//  2. Find ARN-shaped attribute literals not in the resource set.
//  3. Parse each into (TFType, ImportID); warn on unsupported types.
//  4. Call Discoverer.DiscoverByID for each new ref; warn on not-found.
//  5. If anything was added: append to resources, re-run genconfig,
//     re-run driftfix, GOTO 1.
//  6. If nothing was added but unresolved set is non-empty: cycle.
//  7. If unresolved set is empty: return.
//  8. If iterations exceed MaxIterations: fatal.
//
// The "added=0 with unresolved>0" branch is the cycle case. It can
// fire when (a) the discoverer returns ErrNotSupported / ErrNotFound
// for every unresolved ref, in which case the loop should warn and
// not iterate further (we surface this as warnings + clean exit), or
// (b) the discoverer returns valid resources but their ARN/URL
// signatures don't match the literals in generated.tf (a cycle in
// the reference graph). We distinguish (a) from (b) by tracking
// whether *any* successful discovery happened — if not, every
// unresolved ref became a warning and the loop exits clean.
func Run(ctx context.Context, opts Options, resources []imported.ImportedResource) (*Result, error) {
	if opts.Workdir == "" {
		return nil, fmt.Errorf("depchase: Workdir required")
	}
	if opts.Discoverer == nil {
		return nil, fmt.Errorf("depchase: Discoverer required")
	}
	if opts.Pipeline.RunGenconfig == nil {
		return nil, fmt.Errorf("depchase: Pipeline.RunGenconfig required")
	}
	if opts.Pipeline.RunDriftfix == nil {
		return nil, fmt.Errorf("depchase: Pipeline.RunDriftfix required")
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = DefaultMaxIterations
	}

	abs, err := filepath.Abs(opts.Workdir)
	if err != nil {
		return nil, fmt.Errorf("abs workdir: %w", err)
	}
	opts.Workdir = abs
	generatedPath := filepath.Join(opts.Workdir, generatedFile)

	res := &Result{Resources: append([]imported.ImportedResource(nil), resources...)}
	seenWarning := make(map[string]struct{})
	var prevUnresolved []string

	// seenEdges deduplicates Edges across iterations: the same
	// (consumer → discovered) pair can re-surface if the regenerate
	// step rewrites the consumer's HCL without changing the reference.
	seenEdges := make(map[string]struct{})

	for iter := 1; iter <= opts.MaxIterations; iter++ {
		raw, err := os.ReadFile(generatedPath)
		if err != nil {
			return nil, fmt.Errorf("depchase: read generated.tf: %w", err)
		}
		unresolved, consumersByARN, err := findUnresolvedWithConsumers(raw, res.Resources)
		if err != nil {
			return nil, err
		}
		if len(unresolved) == 0 {
			res.GeneratedPath = generatedPath
			return res, nil
		}

		// The "no progress" signal: the same unresolved set surfaced
		// again. We hit this when every ref this iteration's
		// DiscoverByID call returned ErrNotSupported / ErrNotFound
		// (i.e. resources is unchanged), or when the loop adds
		// resources whose ARN signatures don't match the literals.
		// Either way: warn and stop. (Without this guard the loop
		// would still terminate via iteration bound, but the error
		// would be ErrMaxIterations — less actionable than "cycle".)
		if iter > 1 && slices.Equal(unresolved, prevUnresolved) {
			emitUnresolvedAsWarnings(unresolved, res, seenWarning)
			res.GeneratedPath = generatedPath
			// If NO resource was ever added and the unresolved set
			// stabilized at iteration 2, we've simply warned about
			// every ref — that's a clean exit.
			if len(res.Added) == 0 {
				return res, nil
			}
			// Otherwise the loop has added resources but the
			// unresolved set didn't shrink — that's a cycle.
			return res, fmt.Errorf("%w: %d unresolved refs remain after %d iteration(s) (warnings recorded)",
				ErrCyclicDependency, len(unresolved), res.Iterations)
		}
		prevUnresolved = unresolved

		var newSeeds []seed
		for _, arn := range unresolved {
			ref, err := ParseRef(arn)
			if err != nil {
				if errors.Is(err, ErrUnsupportedType) {
					addWarning(res, seenWarning,
						fmt.Sprintf("unsupported ARN type %q (no Terraform discoverer)", arn))
					continue
				}
				addWarning(res, seenWarning,
					fmt.Sprintf("could not parse ARN %q: %v", arn, err))
				continue
			}
			newSeeds = append(newSeeds, seed{arn: arn, ref: ref})
		}
		// Sort seeds for deterministic discovery order (the discoverer
		// has no guaranteed ordering on lookups across types/calls).
		sort.Slice(newSeeds, func(i, j int) bool { return newSeeds[i].arn < newSeeds[j].arn })

		var added []imported.ImportedResource
		for _, s := range newSeeds {
			ir, err := opts.Discoverer.DiscoverByID(ctx, s.ref.TFType, s.ref.ImportID, opts.Region, opts.AccountID)
			if err != nil {
				switch {
				case errors.Is(err, awsdiscover.ErrNotFound):
					addWarning(res, seenWarning,
						fmt.Sprintf("ARN %q (%s): %v", s.arn, s.ref.TFType, err))
				case errors.Is(err, awsdiscover.ErrNotSupported):
					addWarning(res, seenWarning,
						fmt.Sprintf("ARN %q: %s discoverer rejected ID: %v", s.arn, s.ref.TFType, err))
				default:
					return res, fmt.Errorf("DiscoverByID(%s, %s): %w", s.ref.TFType, s.ref.ImportID, err)
				}
				continue
			}
			// Record one edge per (consumer, discovered) pair (#297).
			// consumersByARN was filled from the same generated.tf
			// pass that produced unresolved; every unresolved literal
			// that successfully discovered MUST appear in that map.
			// A defensively-empty consumer slice (e.g. the literal
			// surfaced from a body the walker doesn't handle) just
			// drops the edge — better than panicking on a missing key.
			toAddr := ir.Identity.Address
			if toAddr != "" {
				for _, fromAddr := range consumersByARN[s.arn] {
					recordEdge(res, seenEdges, fromAddr, toAddr)
				}
			}
			added = append(added, ir)
		}

		if len(added) == 0 {
			// No new resources — every unresolved ref turned into a
			// warning. No point regenerating; return clean.
			res.GeneratedPath = generatedPath
			return res, nil
		}

		res.Resources = append(res.Resources, added...)
		res.Added = append(res.Added, added...)

		gcRes, err := opts.Pipeline.RunGenconfig(ctx, res.Resources)
		if err != nil {
			return res, fmt.Errorf("depchase iter %d: regenerate: %w", iter, err)
		}
		// Pick up the populated Attributes the regenerate pass wrote
		// back; the next iteration's FindUnresolved should see them
		// reflected in the generated.tf re-read.
		if gcRes != nil && len(gcRes.Resources) > 0 {
			res.Resources = gcRes.Resources
		}

		if _, err := opts.Pipeline.RunDriftfix(ctx); err != nil {
			return res, fmt.Errorf("depchase iter %d: driftfix: %w", iter, err)
		}
		// Increment only after both pipeline calls succeed so a partial
		// iteration that fails halfway through doesn't claim a complete
		// pass to observability output.
		res.Iterations = iter
	}

	// Loop bound exceeded. Surface the residual unresolved set.
	raw, _ := os.ReadFile(generatedPath)
	residual, _ := FindUnresolved(raw, res.Resources)
	res.GeneratedPath = generatedPath
	return res, fmt.Errorf("%w (%d): %d unresolved ref(s) remain: %v",
		ErrMaxIterations, opts.MaxIterations, len(residual), residual)
}

// seed pairs an unresolved ARN string with the parsed Ref so the
// loop can carry both through to discovery + warning paths.
type seed struct {
	arn string
	ref Ref
}

// recordEdge appends a (from, to) GraphEdge to res.Edges if the same
// pair hasn't been recorded before in this run. Dedup is per-pair so
// a consumer that references the same target twice (or that
// resurfaces across iterations because the regenerate stage rewrote
// the HCL) only contributes one edge to graph.json.
//
// The Edges slice is kept in sorted (From, To) order so the on-disk
// graph.json is byte-identical across runs for the same input, even
// though insertion order is non-deterministic in
// findUnresolvedWithConsumers's map iteration. We sort on insertion
// rather than at write-time so any future caller that reads
// res.Edges directly sees the same stable order.
func recordEdge(res *Result, seen map[string]struct{}, from, to string) {
	key := from + "\x00" + to
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	res.Edges = append(res.Edges, GraphEdge{From: from, To: to})
	sort.Slice(res.Edges, func(i, j int) bool {
		if res.Edges[i].From != res.Edges[j].From {
			return res.Edges[i].From < res.Edges[j].From
		}
		return res.Edges[i].To < res.Edges[j].To
	})
}

// addWarning appends to Warnings if the same message hasn't been
// emitted before in this run. Dedup is per-message string, not
// per-ARN, because two ARNs could legitimately produce the same
// "unsupported ARN type X" warning under their service+rtype.
func addWarning(res *Result, seen map[string]struct{}, msg string) {
	if _, ok := seen[msg]; ok {
		return
	}
	seen[msg] = struct{}{}
	res.Warnings = append(res.Warnings, msg)
}

// emitUnresolvedAsWarnings is called when the loop detects a stable
// unresolved set after a no-progress iteration. We surface each
// remaining literal as a warning so the operator sees what wasn't
// chased; the caller decides whether to treat the run as success or
// failure based on whether anything had been successfully added.
func emitUnresolvedAsWarnings(unresolved []string, res *Result, seen map[string]struct{}) {
	for _, arn := range unresolved {
		addWarning(res, seen, fmt.Sprintf("unresolved ARN reference (stable across iterations): %q", arn))
	}
}
