package depchase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
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
// Resources slice reflects the resources that survived config
// generation. It may populate Attributes on the input resources and it
// may drop entries Terraform could not render (for example orphan
// imports). Depchase treats newly discovered resources that disappear
// from this result as unresolvable warnings so the loop does not
// oscillate on references to provider-generated gaps.
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

	// AdoptionPolicy bounds the closure (presets#864). For each discovered,
	// importable dependency it decides ADOPT (add to the managed import set,
	// historical behavior) vs REFERENCE (leave the literal in the consumer's
	// HCL and record a depchase_reference_retained warning). Nil means
	// adopt-all — the historical unbounded closure — so existing callers are
	// unaffected. reverse-import constructs a SelectionScopePolicy here when
	// Options.BoundClosureToSelection is set. See
	// docs/depchase-closure-contract.md.
	AdoptionPolicy AdoptionPolicy

	// Stdout, when non-nil, receives a concise per-iteration progress
	// line ("depchase: iteration k: discovered N resource(s)…") so a
	// long-running caller — the Mars reverse-import job — can surface live
	// progress through the chase loop instead of going silent while the
	// nested genconfig/driftfix re-runs execute. Nil discards progress
	// (the historical behavior). The nested terraform subprocess output
	// is streamed separately by the Pipeline closures the caller wires.
	Stdout io.Writer
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
	// Warnings are structured (presets#854): each carries a stable Code plus the
	// render inputs, with String() reproducing the historical prose for the
	// CLI/discover output. reverseimport maps Code → job.Diagnostic.Code so
	// Reliable's diagnostics surface can classify without pattern-matching prose.
	Warnings []Warning

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

	res := &Result{Resources: slices.Clone(resources)}
	seenWarning := make(map[warningKey]struct{})
	ignoredARNs := make(map[string]struct{})
	var prevUnresolved []string

	// chased records every nested (class A) / nonARN (class B) literal that has
	// been seeded once, regardless of discovery outcome (F2). Such literals are
	// never rewritten by crossref, so after their single chase they may remain
	// textually unresolved forever (e.g. a version-qualified lambda ARN whose
	// adopted NativeIDs never byte-match). Excluding them from re-seeding and
	// from the unresolved-set stability comparison prevents both a spurious
	// ErrCyclicDependency on a previously-clean run and pointless re-discovery
	// every iteration. Their warning already documents that the literal is
	// retained, so dropping them from the unresolved accounting loses nothing.
	chased := make(map[string]struct{})

	// seenAddedAddr deduplicates res.Added across iterations by resource
	// address: the same target can be referenced by two different literals in
	// one iteration (e.g. a KMS key by full ARN nested AND by bare UUID in a
	// kms_key_id attr, F8). We record every consumer edge but adopt the resource
	// once.
	seenAddedAddr := make(map[string]struct{})

	// seenEdges deduplicates Edges across iterations: the same
	// (consumer → discovered) pair can re-surface if the regenerate
	// step rewrites the consumer's HCL without changing the reference.
	seenEdges := make(map[string]struct{})

	// provByAddr accumulates the closure provenance (presets#864) for every
	// resource the loop adopts, keyed by Terraform address. It is applied to
	// res.Resources at the end of each iteration (after genconfig replaces the
	// slice) so imported.json — which reverse-import writes from res.Resources —
	// carries a pulled_in_by reason on each auto-included resource regardless of
	// genconfig round-tripping the resource list. Consumer edges are unioned
	// across literals/iterations that resolve to the same target.
	provByAddr := make(map[string]*imported.PulledInBy)

	for iter := 1; iter <= opts.MaxIterations; iter++ {
		raw, err := os.ReadFile(generatedPath)
		if err != nil {
			return nil, fmt.Errorf("depchase: read generated.tf: %w", err)
		}
		// Pass the terminal/ignored skip-set (ignoredARNs ∪ chased) into the walk
		// so a literal we would only filter back out is never recorded in the
		// first place (F2/G5). Empty on iteration 1, so first-pass edges/warnings
		// are identical to recording-then-filtering; it grows as refs are
		// ignored (unsupported/not-found) or chased (terminal-by-design). The
		// resulting scan.unresolved therefore already excludes them — no
		// post-filter needed for the len==0 convergence check, the stability
		// comparison, or re-seeding below.
		skip := unionSkip(ignoredARNs, chased)
		scan, err := scanGenerated(raw, res.Resources, skip)
		if err != nil {
			return nil, err
		}
		unresolved := scan.unresolved
		consumersByARN := scan.consumers
		if len(unresolved) == 0 {
			res.GeneratedPath = generatedPath
			sortEdges(res)
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
				sortEdges(res)
				return res, nil
			}
			// Otherwise the loop has added resources but the
			// unresolved set didn't shrink — that's a cycle.
			sortEdges(res)
			return res, fmt.Errorf("%w: %d unresolved refs remain after %d iteration(s) (warnings recorded)",
				ErrCyclicDependency, len(unresolved), res.Iterations)
		}
		prevUnresolved = unresolved

		// Build a consumer-address → region index (after the early returns, G5)
		// so a class-B bare-UUID seed (which carries no region of its own) can be
		// discovered in the CONSUMER's region — a KMS CMK is same-region as its
		// SSE consumer — rather than blindly in the run's primary region (F3).
		regionByAddr := regionIndex(res.Resources)

		var newSeeds []seed
		for _, arn := range unresolved {
			// A literal that ALSO appears top-level keeps top-level chase
			// semantics (fatal-on-error, non-terminal): its top-level occurrence
			// is rewritten by crossref, so resolution is expected there even if
			// it is additionally nested elsewhere.
			_, isTopLevel := scan.topLevel[arn]
			// presets#834 class A — an ARN literal the historical top-level
			// pass would have MISSED (nested inside a block or a
			// list/object expression) is now surfaced. Emit the precise
			// detection warning up front so class A is never silent,
			// regardless of whether the chase below succeeds, fails, or hits
			// an unsupported type. The literal itself is not rewritten (the
			// genconfig crossref pass is also top-level-only), but the target
			// is chased and — when adoptable — emitted, improving graph
			// fidelity. For a dual-occurrence literal (also top-level) the
			// nested consumer is still warned about here, but the seed is tagged
			// top-level so its failure policy matches the top-level occurrence.
			class := classTopLevel
			if nh, ok := scan.nested[arn]; ok {
				if !isTopLevel {
					class = classNested
				}
				addWarning(res, seenWarning, Warning{
					Code:     CodeNestedRefLiteral,
					Literal:  arn,
					Consumer: nh.addr,
					Path:     nh.path,
				})
			}
			// presets#834 class B — a curated, non-ARN identifier reference
			// (a KMS KeyId UUID in kms_key_id / kms_master_key_id). isARNLiteral
			// never considered it, so it was silent. The KMS Cloud Control
			// DiscoverByID accepts a bare KeyId, so we chase it with a
			// pre-resolved Ref (no ARN to ParseRef). A bare UUID carries no
			// region of its own, so the Region is left EMPTY here and filled
			// from the consumer resource's region below (F3), falling back to
			// the run's primary region only if the consumer has none.
			if nq, ok := scan.nonARN[arn]; ok {
				chased[arn] = struct{}{}
				addWarning(res, seenWarning, Warning{
					Code:     CodeNonARNRefLiteral,
					Literal:  arn,
					Consumer: nq.addr,
					Attr:     nq.attr,
					TFType:   nq.tfType,
				})
				newSeeds = append(newSeeds, seed{
					arn:   arn,
					class: classNonARN,
					ref:   Ref{TFType: nq.tfType, ImportID: arn, Region: regionByAddr[nq.addr]},
				})
				continue
			}
			// Mark a purely-nested literal terminal-by-design before ParseRef so
			// it is chased exactly once regardless of the parse/discovery outcome.
			if class == classNested {
				chased[arn] = struct{}{}
			}
			ref, err := ParseRef(arn)
			if err != nil {
				if errors.Is(err, ErrUnsupportedType) {
					addWarning(res, seenWarning, Warning{
						Code:    CodeUnsupportedRef,
						Literal: arn,
					})
					ignoredARNs[arn] = struct{}{}
					continue
				}
				addWarning(res, seenWarning, Warning{
					Code:    CodeUnparseableRef,
					Literal: arn,
					Reason:  err.Error(),
				})
				ignoredARNs[arn] = struct{}{}
				continue
			}
			newSeeds = append(newSeeds, seed{arn: arn, class: class, ref: ref})
		}
		// Sort seeds for deterministic discovery order (the discoverer
		// has no guaranteed ordering on lookups across types/calls).
		sort.Slice(newSeeds, func(i, j int) bool { return newSeeds[i].arn < newSeeds[j].arn })

		var added []imported.ImportedResource
		var discoveries []discoveredResource
		// seenIterAddr dedups the resources ADDED this iteration by address so
		// two seeds resolving to the same target (F8) adopt it once. Consumer
		// edges are still recorded for both via `discoveries`.
		seenIterAddr := make(map[string]struct{})
		for _, s := range newSeeds {
			// Discover each ref in ITS OWN region (the ARN's 4th segment),
			// falling back to the run's primary region for global/region-less
			// ARNs (IAM/CloudFront/Route53/S3, where Region is empty). This is
			// what makes the chase correct for multi-region imports: a
			// cross-region reference (e.g. a us-east-1 resource pointing at a
			// us-west-2 KMS key) must hit the target's region, not the primary.
			region := s.ref.Region
			if region == "" {
				region = opts.Region
			}
			ir, err := opts.Discoverer.DiscoverByID(ctx, s.ref.TFType, s.ref.ImportID, region, opts.AccountID)
			if err != nil {
				// Check both AWS and GCP sentinels so the GCP path
				// surfaces the same warn-and-continue UX as AWS. The two
				// `errors.New("...")` instances live in different
				// packages, so a single `errors.Is(err, awsdiscover.X)`
				// would fall through to the fatal default branch on a
				// genuine GCP not-found / not-supported and abort the
				// whole run — wrong outcome for a per-ARN issue.
				switch {
				case errors.Is(err, awsdiscover.ErrNotFound), errors.Is(err, gcpdiscover.ErrNotFound):
					addWarning(res, seenWarning, Warning{
						Code:    CodeRefNotFound,
						Literal: s.arn,
						TFType:  s.ref.TFType,
						Reason:  err.Error(),
					})
					ignoredARNs[s.arn] = struct{}{}
				case errors.Is(err, awsdiscover.ErrNotSupported), errors.Is(err, gcpdiscover.ErrNotSupported):
					addWarning(res, seenWarning, Warning{
						Code:    CodeDiscovererRejected,
						Literal: s.arn,
						TFType:  s.ref.TFType,
						Reason:  err.Error(),
					})
					ignoredARNs[s.arn] = struct{}{}
				default:
					// A non-sentinel (hard) discovery error aborts the run only
					// for a top-level seed, whose resolution is expected. For a
					// nested/nonARN fidelity-enhancement seed it must NOT fail an
					// import that previously (silently) succeeded — degrade to a
					// warning and leave the literal (F1).
					if s.class.terminal() {
						addWarning(res, seenWarning, Warning{
							Code:    CodeDiscoverError,
							Literal: s.arn,
							TFType:  s.ref.TFType,
							Reason:  err.Error(),
						})
						ignoredARNs[s.arn] = struct{}{}
						break
					}
					sortEdges(res)
					return res, fmt.Errorf("DiscoverByID(%s, %s): %w", s.ref.TFType, s.ref.ImportID, err)
				}
				continue
			}
			// Un-importability gate (presets#834). A ref can point at a
			// resource that DiscoverByID finds but that is inherently
			// un-adoptable into customer Terraform state — an AWS-managed KMS
			// key (KeyManager=AWS), a service-linked IAM role
			// (role/aws-service-role/…), a service-managed ENI, an
			// already-InsideOut-imported resource, etc. Adding it would only
			// have RunGenconfig drop it again (no generated body / provider
			// import failure), surfacing the opaque "discovered … but generated
			// config omitted it" warning and burning a full regenerate cycle.
			// Consult the SAME classifier discovery and the genconfig prune use
			// (imported.UnimportableReason, #709) so all three agree, leave the
			// literal knowingly, and emit a precise reason instead. The observed
			// aws_kms_key / aws_iam_role literals from the account-141812438321
			// import (#834) are exactly this class.
			if reason := imported.UnimportableReason(ir); reason != "" {
				addWarning(res, seenWarning, Warning{
					Code:    CodeUnimportableTarget,
					Literal: s.arn,
					TFType:  s.ref.TFType,
					Reason:  reason,
				})
				ignoredARNs[s.arn] = struct{}{}
				continue
			}
			// Closure-contract bound (presets#864). The target is importable and
			// unclaimed (the UnimportableReason gate above already rejects
			// InsideOut-claimed and inherently un-adoptable targets), so the only
			// remaining question is scope: does the configured AdoptionPolicy
			// want this dependency ADOPTED or represented as a REFERENCE? A
			// declined dependency is left as its literal in the consumer's HCL —
			// already valid Terraform for a concrete external identifier — and is
			// NOT adopted. Mark it terminal-by-design (chased) and ignored so it
			// drains from the unresolved accounting and the loop converges
			// without burning a regenerate cycle for it. A nil policy (the
			// default) adopts everything, preserving historical behavior.
			if decision := decideAdoption(opts.AdoptionPolicy, ir, consumersByARN[s.arn]); !decision.Adopt {
				consumer := ""
				if cs := consumersByARN[s.arn]; len(cs) > 0 {
					consumer = cs[0]
				}
				addWarning(res, seenWarning, Warning{
					Code:     CodeReferenceRetained,
					Literal:  s.arn,
					TFType:   s.ref.TFType,
					Consumer: consumer,
					Reason:   decision.Reason,
				})
				chased[s.arn] = struct{}{}
				ignoredARNs[s.arn] = struct{}{}
				continue
			}
			// Record the discovery for edge accounting regardless, but adopt the
			// resource into the import set at most once per address (F8): a
			// second seed pointing at the same target (same key by ARN and by
			// bare UUID) must not double-add it, though its consumer edge is
			// still recorded via `discoveries` → `kept` below.
			discoveries = append(discoveries, discoveredResource{seed: s, resource: ir})
			addr := ir.Identity.Address
			if addr != "" {
				if _, dup := seenIterAddr[addr]; dup {
					continue
				}
				if _, prior := seenAddedAddr[addr]; prior {
					continue
				}
				seenIterAddr[addr] = struct{}{}
			}
			added = append(added, ir)
		}

		if len(added) == 0 {
			// No new resources — every unresolved ref turned into a
			// warning. No point regenerating; return clean.
			res.GeneratedPath = generatedPath
			sortEdges(res)
			return res, nil
		}

		progressf(opts.Stdout, "depchase: iteration %d: discovered %d dependency resource(s), regenerating config…\n", iter, len(added))

		res.Resources = append(res.Resources, added...)

		gcRes, err := opts.Pipeline.RunGenconfig(ctx, res.Resources)
		if err != nil {
			sortEdges(res)
			return res, fmt.Errorf("depchase iter %d: regenerate: %w", iter, err)
		}
		// Pick up the populated Attributes the regenerate pass wrote
		// back; the next iteration's FindUnresolved should see them
		// reflected in the generated.tf re-read.
		if gcRes != nil {
			res.Resources = gcRes.Resources
		}
		kept, dropped := partitionDiscoveries(discoveries, res.Resources)
		for _, d := range dropped {
			ignoredARNs[d.seed.arn] = struct{}{}
			removeEdgesTo(res, seenEdges, d.resource.Identity.Address)
			// Consumer carries the omitted (discovered) resource's address so the
			// folded Diagnostic.Field is attributable (G4). String() renders it in
			// the "discovered as %s" slot — byte-identical to the pre-G4
			// Reason-held address.
			addWarning(res, seenWarning, Warning{
				Code:     CodeConfigOmitted,
				Literal:  d.seed.arn,
				TFType:   d.seed.ref.TFType,
				Consumer: d.resource.Identity.Address,
			})
		}
		for _, d := range kept {
			// Record one edge per (consumer, discovered) pair (#297).
			// consumersByARN was filled from the same generated.tf
			// pass that produced unresolved; every unresolved literal
			// that successfully discovered and survived regeneration
			// MUST appear in that map. A defensively-empty consumer
			// slice just drops the edge — better than panicking on a
			// missing key.
			toAddr := d.resource.Identity.Address
			if toAddr != "" {
				for _, fromAddr := range consumersByARN[d.seed.arn] {
					recordEdge(res, seenEdges, fromAddr, toAddr)
				}
				// Record the closure provenance for this adoption (presets#864):
				// the consumers that referenced it become PulledInBy.Consumers,
				// unioned across every literal/iteration that resolves to the
				// same address (F8) so a target referenced twice carries all its
				// consumers. Recorded even before the F8 dedup below so both
				// seeds' consumers land on the single Added entry.
				mergeProvenance(provByAddr, toAddr, consumersByARN[d.seed.arn])
				// Adopt the resource into res.Added once per address (F8): both
				// seeds' edges above are still recorded, but two literals
				// resolving to the same target contribute a single Added entry.
				if _, dup := seenAddedAddr[toAddr]; dup {
					continue
				}
				seenAddedAddr[toAddr] = struct{}{}
			}
			d.resource.PulledInBy = provByAddr[toAddr]
			res.Added = append(res.Added, d.resource)
		}
		// Stamp the accumulated provenance onto res.Resources (presets#864). Done
		// every iteration because genconfig replaced the slice above; provByAddr
		// accumulates, so re-applying it to the current slice keeps every prior
		// iteration's adoption stamped through to the converged final set that
		// reverse-import serializes into imported.json.
		stampProvenance(res.Resources, provByAddr)

		if _, err := opts.Pipeline.RunDriftfix(ctx); err != nil {
			sortEdges(res)
			return res, fmt.Errorf("depchase iter %d: driftfix: %w", iter, err)
		}
		// Increment only after both pipeline calls succeed so a partial
		// iteration that fails halfway through doesn't claim a complete
		// pass to observability output.
		res.Iterations = iter
	}

	// Loop bound exceeded. Surface the residual unresolved set so the
	// operator can see exactly which references failed to converge.
	// Capture and surface FindUnresolved's error if the residual
	// enumeration itself failed: dropping it on the floor produced a
	// misleading "0 unresolved ref(s) remain" message even when the
	// residual could not actually be enumerated.
	raw, _ := os.ReadFile(generatedPath)
	residual, residualErr := FindUnresolved(raw, res.Resources)
	// Exclude ignored + terminal-by-design (chased) literals from the residual
	// accounting so the operator-facing "N unresolved ref(s) remain" message
	// lists only genuinely-unconverged references, not nested/nonARN literals
	// that are retained by design (F2) or already warned-and-ignored.
	residual = filterIgnoredARNs(residual, ignoredARNs)
	residual = filterIgnoredARNs(residual, chased)
	res.GeneratedPath = generatedPath
	residualStr := strings.Join(residual, ", ")
	sortEdges(res)
	if residualErr != nil {
		return res, fmt.Errorf("%w (%d): %d unresolved ref(s) remain: %s; (residual enumeration error: %v)",
			ErrMaxIterations, opts.MaxIterations, len(residual), residualStr, residualErr)
	}
	return res, fmt.Errorf("%w (%d): %d unresolved ref(s) remain: %s",
		ErrMaxIterations, opts.MaxIterations, len(residual), residualStr)
}

// seedClass records where a seed's literal was found so the loop can apply the
// right failure policy. A topLevel literal is one the historical top-level pass
// found and that the genconfig crossref rewrites — its discovery is EXPECTED to
// resolve, so a hard discovery error is fatal. A nested (class A) or nonARN
// (class B) literal is a fidelity ENHANCEMENT surfaced by presets#834: it is
// never rewritten in place, so a hard discovery error must not abort an import
// that previously (silently) succeeded — it degrades to a warning (F1). Both
// nested/nonARN literals are also terminal-by-design: once chased, they may
// legitimately remain textually unresolved forever, so the loop chases each
// exactly once and excludes it from cycle detection (F2).
type seedClass uint8

const (
	classTopLevel seedClass = iota
	classNested
	classNonARN
)

func (c seedClass) String() string {
	switch c {
	case classNested:
		return "nested"
	case classNonARN:
		return "non-ARN"
	default:
		return "top-level"
	}
}

// terminal reports whether the class is terminal-by-design (chased once, never
// re-seeded, excluded from cycle detection) and fail-open on discovery error.
func (c seedClass) terminal() bool { return c == classNested || c == classNonARN }

// seed pairs an unresolved literal with the parsed Ref and its origin class so
// the loop can carry all three through to discovery + warning paths.
type seed struct {
	arn   string
	class seedClass
	ref   Ref
}

type discoveredResource struct {
	seed     seed
	resource imported.ImportedResource
}

// regionIndex maps each resource address to its identity Region so a class-B
// bare-UUID seed can inherit the region of the resource that consumes it (F3).
// Addresses with an empty Region are still indexed (as "") so the caller's
// primary-region fallback applies.
func regionIndex(resources []imported.ImportedResource) map[string]string {
	idx := make(map[string]string, len(resources))
	for _, r := range resources {
		if r.Identity.Address != "" {
			idx[r.Identity.Address] = r.Identity.Region
		}
	}
	return idx
}

// unionSkip returns the union of two literal sets as the walk's skip-set
// (ignoredARNs ∪ chased). Returns nil when both are empty so the iteration-1
// walk records everything minus resolved (edges/warnings unchanged); otherwise a
// fresh map so a later mutation of either input doesn't alias the walk's view.
func unionSkip(a, b map[string]struct{}) map[string]struct{} {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func filterIgnoredARNs(unresolved []string, ignored map[string]struct{}) []string {
	if len(unresolved) == 0 || len(ignored) == 0 {
		return unresolved
	}
	out := unresolved[:0]
	for _, arn := range unresolved {
		if _, ok := ignored[arn]; ok {
			continue
		}
		out = append(out, arn)
	}
	return out
}

func partitionDiscoveries(discoveries []discoveredResource, resources []imported.ImportedResource) (kept, dropped []discoveredResource) {
	if len(discoveries) == 0 {
		return nil, nil
	}
	live := make(map[string]struct{}, len(resources))
	for _, r := range resources {
		if r.Identity.Address != "" {
			live[r.Identity.Address] = struct{}{}
		}
	}
	for _, d := range discoveries {
		if _, ok := live[d.resource.Identity.Address]; ok {
			kept = append(kept, d)
			continue
		}
		dropped = append(dropped, d)
	}
	return kept, dropped
}

// recordEdge appends a (from, to) GraphEdge to res.Edges if the same
// pair hasn't been recorded before in this run. Dedup is per-pair so
// a consumer that references the same target twice (or that
// resurfaces across iterations because the regenerate stage rewrote
// the HCL) only contributes one edge to graph.json.
//
// The Edges slice is appended unsorted; Run calls sortEdges once at
// each return point so the on-disk graph.json is byte-identical
// across runs for the same input, even though insertion order is
// non-deterministic in findUnresolvedWithConsumers's map iteration.
// (The previous shape sorted on every insertion — O(n^2 log n) over
// the loop. Deferring the sort to the result-finalization step is
// equivalent for callers that read res.Edges only after Run returns.)
func recordEdge(res *Result, seen map[string]struct{}, from, to string) {
	key := from + "\x00" + to
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	res.Edges = append(res.Edges, GraphEdge{From: from, To: to})
}

func removeEdgesTo(res *Result, seen map[string]struct{}, to string) {
	if to == "" || len(res.Edges) == 0 {
		return
	}
	out := res.Edges[:0]
	for _, edge := range res.Edges {
		key := edge.From + "\x00" + edge.To
		if edge.To == to {
			delete(seen, key)
			continue
		}
		out = append(out, edge)
	}
	res.Edges = out
}

// sortEdges sorts res.Edges in (From, To) order. Called once per
// successful Run exit so the visible (post-Run) shape is the same as
// the previous per-insertion-sort behavior. A regression that adds a
// new return path without a sortEdges call would surface as a
// flaky-graph.json test failure; the writeGraphManifest re-sort is a
// belt-and-braces guard against that.
func sortEdges(res *Result) {
	sort.Slice(res.Edges, func(i, j int) bool {
		if res.Edges[i].From != res.Edges[j].From {
			return res.Edges[i].From < res.Edges[j].From
		}
		return res.Edges[i].To < res.Edges[j].To
	})
}

// addWarning appends a structured Warning to Warnings if one with the same
// (Code, Literal, Consumer) identity hasn't been emitted before in this run
// (presets#854). Keying on the tuple rather than the rendered string means a
// prose/path change can't duplicate a warning across iterations, while two
// different ARNs still produce two "unsupported ARN type" warnings (distinct
// Literal).
func addWarning(res *Result, seen map[warningKey]struct{}, w Warning) {
	k := w.key()
	if _, ok := seen[k]; ok {
		return
	}
	seen[k] = struct{}{}
	res.Warnings = append(res.Warnings, w)
}

// progressf writes a human-readable progress line to w when w is
// non-nil. Best-effort: a write error to a progress sink must never
// affect the chase result, so the error is intentionally ignored.
func progressf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

// emitUnresolvedAsWarnings is called when the loop detects a stable
// unresolved set after a no-progress iteration. We surface each
// remaining literal as a warning so the operator sees what wasn't
// chased; the caller decides whether to treat the run as success or
// failure based on whether anything had been successfully added.
func emitUnresolvedAsWarnings(unresolved []string, res *Result, seen map[warningKey]struct{}) {
	for _, arn := range unresolved {
		addWarning(res, seen, Warning{Code: CodeUnresolvedStable, Literal: arn})
	}
}
