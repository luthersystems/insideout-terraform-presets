package depchase

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// generatedFile is the conventional filename driftfix and depchase
// agree on. Mirrors the same constant in genconfig/driftfix so all
// three subpackages parse the same artifact.
const generatedFile = "generated.tf"

// FindUnresolved walks the cleaned generated.tf and returns the
// deterministic-sorted, deduplicated set of ARN-shaped string-literal
// attribute values that do NOT match any in-batch resource's known
// identity (ARN/URL/ImportID/NativeIDs).
//
// The walker mirrors genconfig/crossref.go's HCL traversal: parse with
// hclsyntax.ParseConfig, iterate `resource` blocks (two-label blocks),
// and inspect each attribute — recursing into nested blocks and
// collection expressions — considering only pure double-quoted string
// literals via pureStringLiteral. Anything more complex
// (interpolations, function calls, traversals) is left alone — the
// dep-chase contract is conservative: only act on values we can be
// certain are concrete external references.
//
// The "resolved set" is built from the same triple that
// genconfig/crossref.go uses: NativeIDs[arn], NativeIDs[url], and
// ImportID. A literal that matches any of those is considered
// in-batch and therefore not unresolved.
func FindUnresolved(raw []byte, resources []imported.ImportedResource) ([]string, error) {
	scan, err := scanGenerated(raw, resources, nil)
	if err != nil {
		return nil, err
	}
	return scan.unresolved, nil
}

// scanResult bundles everything one generated.tf pass surfaces for the
// chase loop. unresolved is the deterministic-sorted, deduplicated set of
// reference literals to chase — top-level ARN hits (historical), class-A
// nested ARN hits, and class-B curated non-ARN identifier hits, unified into
// one keyspace so the Run loop's cycle detection / ignore filtering treats
// them identically. consumers maps each literal to its sorted consumer
// addresses (for #297 graph edges) — every distinct (literal → consumer) pair
// the single walk observed, so no consumer edge is lost when a literal is
// referenced by multiple resources or appears both top-level and nested (F4).
// nested / nonARN carry the per-literal location + classification metadata so
// Run can emit the precise class-A / class-B warnings (presets#834). topLevel
// is the set of literals the depth-0 direct-attribute pass found AS A STRICT,
// crossref-rewritable quoted literal — Run consults it to decide a hit's origin
// class (a literal that is ALSO strict-top-level keeps top-level chase semantics
// rather than the terminal-by-design nested/nonARN semantics). A depth-0 ARN
// that is NOT a strict quoted literal (heredoc / escape-bearing) is recorded as
// nested, never here, because crossref cannot rewrite it (G1).
type scanResult struct {
	unresolved []string
	consumers  map[string][]string
	nested     map[string]nestedHit
	nonARN     map[string]nonARNHit
	topLevel   map[string]struct{}
}

// scanGenerated is the shared parser pass behind the public FindUnresolved
// and the Run loop. It runs a SINGLE hclsyntax parse and a SINGLE recursive
// walk per resource body (presets#853): depth-0 direct attributes are the
// degenerate top-level case (historical ARN semantics), while nested blocks
// and collection-expression trees surface the class-A / class-B hits of
// presets#834. All hits flow through one bodyWalker into one unresolved set
// plus the consumer-address edges the Run loop turns into (consumer →
// discovered) graph edges (#297).
//
// Before #853 this function parsed generated.tf TWICE — once with hclwrite for
// the top-level pass and once with hclsyntax for the nested walk — stitched by
// a top-level-set seam and backed by two mirrored "pure string literal"
// extractors that could drift on edge cases (heredocs). The unified walk keeps
// a single extractor (pureStringLiteral) and halves the per-iteration parse
// cost.
// skip is an optional caller-supplied set of literals (ignoredARNs ∪ chased)
// the walk must NOT record — treated like resolved. The Run loop passes it so a
// terminal/ignored literal isn't re-recorded then re-filtered every iteration
// (G5); nil (the FindUnresolved path) records everything minus resolved.
func scanGenerated(raw []byte, resources []imported.ImportedResource, skip map[string]struct{}) (scanResult, error) {
	resolved := buildResolvedSet(resources)
	file, diags := hclsyntax.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return scanResult{}, fmt.Errorf("depchase: parse generated.tf: %s", diags.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return scanResult{}, fmt.Errorf("depchase: unexpected body type %T", file.Body)
	}

	w := &bodyWalker{
		src:       raw,
		resolved:  resolved,
		skip:      skip,
		topLevel:  map[string]struct{}{},
		nested:    map[string]nestedHit{},
		nonARN:    map[string]nonARNHit{},
		consumers: map[string]map[string]struct{}{},
	}
	for _, blk := range body.Blocks {
		if blk.Type != "resource" || len(blk.Labels) != 2 {
			continue
		}
		w.addr = blk.Labels[0] + "." + blk.Labels[1]
		w.walkBody(blk.Body, "", 0)
	}

	// Every recorded hit registers a consumer edge, so the consumer map's
	// keyset IS the unresolved literal set. Derive both the sorted unresolved
	// slice and the flattened, deterministic-sorted consumer slices in one
	// pass so callers (and tests) see byte-stable output.
	out := make([]string, 0, len(w.consumers))
	flat := make(map[string][]string, len(w.consumers))
	for lit, set := range w.consumers {
		out = append(out, lit)
		addrs := make([]string, 0, len(set))
		for a := range set {
			addrs = append(addrs, a)
		}
		sort.Strings(addrs)
		flat[lit] = addrs
	}
	sort.Strings(out)

	return scanResult{unresolved: out, consumers: flat, nested: w.nested, nonARN: w.nonARN, topLevel: w.topLevel}, nil
}

// isARNLiteral is the cheap "is this value worth feeding to ParseRef"
// test. AWS ARNs are colon-separated and follow
// `arn:<partition>:<service>:<region>:<account>:<resource>` — six
// fields, five colons minimum. Pre-filtering to that shape keeps
// malformed string literals (e.g. `arn:foo`) out of the warnings
// stream rather than producing a noisy "could not parse ARN"
// warning per literal per iteration. ParseRef does the real
// validation; this is just the "worth trying" gate.
func isARNLiteral(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "arn:") {
		return false
	}
	// Count colons cheaply; stdlib `strings.Count` is one allocation-
	// free pass. The fifth colon may sit inside the resource portion
	// (e.g. logs `log-group:<name>:*`), so we require AT LEAST 5.
	return strings.Count(s, ":") >= 5
}

// buildResolvedSet inverts the in-batch resource list into a set of
// known identifier strings. Mirrors the inputs to
// genconfig/crossref.go:buildCrossRefIndex (NativeIDs[arn],
// NativeIDs[url], ImportID) so dep-chase and crossref agree on what
// counts as "already in the batch."
func buildResolvedSet(resources []imported.ImportedResource) map[string]struct{} {
	set := make(map[string]struct{}, 3*len(resources))
	for _, r := range resources {
		if arn := r.Identity.NativeIDs["arn"]; arn != "" {
			set[arn] = struct{}{}
		}
		if url := r.Identity.NativeIDs["url"]; url != "" {
			set[url] = struct{}{}
		}
		if id := r.Identity.ImportID; id != "" {
			set[id] = struct{}{}
		}
	}
	return set
}
