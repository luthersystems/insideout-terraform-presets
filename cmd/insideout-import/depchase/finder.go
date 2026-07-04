package depchase

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"

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
// hclwrite.ParseConfig, iterate `resource` blocks (two-label blocks),
// inspect each top-level attribute, and consider only pure
// double-quoted string literals via stringLiteralValue. Anything more
// complex (interpolations, function calls, lists) is left alone — the
// dep-chase contract is conservative: only act on values we can be
// certain are concrete external references.
//
// The "resolved set" is built from the same triple that
// genconfig/crossref.go uses: NativeIDs[arn], NativeIDs[url], and
// ImportID. A literal that matches any of those is considered
// in-batch and therefore not unresolved.
func FindUnresolved(raw []byte, resources []imported.ImportedResource) ([]string, error) {
	scan, err := scanGenerated(raw, resources)
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
// across BOTH passes, so no consumer edge is lost when a literal is referenced
// by multiple resources or appears both top-level and nested (F4). nested /
// nonARN carry the per-literal location + classification metadata so Run can
// emit the precise class-A / class-B warnings (presets#834). topLevel is the
// set of literals the historical top-level ARN pass found — Run consults it to
// decide a hit's origin class (a literal that is ALSO top-level keeps top-level
// chase semantics rather than the terminal-by-design nested/nonARN semantics).
type scanResult struct {
	unresolved []string
	consumers  map[string][]string
	nested     map[string]nestedHit
	nonARN     map[string]nonARNHit
	topLevel   map[string]struct{}
}

// scanGenerated is the shared parser pass behind the public FindUnresolved
// and the Run loop. It runs the historical top-level ARN pass (hclwrite) plus
// the presets#834 nested/non-ARN walk (hclsyntax), unifies their hits into one
// unresolved set, and records the consumer addresses the Run loop turns into
// (consumer → discovered) graph edges (#297).
func scanGenerated(raw []byte, resources []imported.ImportedResource) (scanResult, error) {
	resolved := buildResolvedSet(resources)
	f, diags := hclwrite.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return scanResult{}, fmt.Errorf("depchase: parse generated.tf: %s", diags.Error())
	}

	seen := make(map[string]struct{})
	consumers := make(map[string]map[string]struct{}) // literal → set of addresses
	addConsumer := func(lit, addr string) {
		seen[lit] = struct{}{}
		set, ok := consumers[lit]
		if !ok {
			set = make(map[string]struct{})
			consumers[lit] = set
		}
		set[addr] = struct{}{}
	}

	// topLevel is the set of literals the historical top-level pass finds. Built
	// independently (not a post-hoc copy of `seen`, which the nested merge below
	// mutates) so it stays a pure top-level snapshot the Run loop can consult for
	// origin-class tagging.
	topLevel := make(map[string]struct{})
	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		if len(blk.Labels()) != 2 {
			continue
		}
		addr := blk.Labels()[0] + "." + blk.Labels()[1]
		hits := collectFromBodyWithHits(blk.Body(), resolved)
		for _, lit := range hits {
			topLevel[lit] = struct{}{}
			addConsumer(lit, addr)
		}
	}

	// presets#834 classes A + B: nested ARN literals and curated non-ARN
	// identifier references. Merge their literals into the same unresolved
	// keyspace and fold EVERY (literal → consumer) pair the nested walk found
	// into the consumer map — not just each literal's representative winner —
	// so multi-consumer and dual-occurrence (top-level + nested) edges survive
	// (F4).
	nested, nonARN, nestedConsumers, err := scanNestedAndNonARN(raw, resolved)
	if err != nil {
		return scanResult{}, err
	}
	for lit, addrs := range nestedConsumers {
		for addr := range addrs {
			addConsumer(lit, addr)
		}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)

	// Flatten consumers map into deterministic-sorted slices so callers
	// (and tests) see byte-stable output.
	flat := make(map[string][]string, len(consumers))
	for arn, set := range consumers {
		addrs := make([]string, 0, len(set))
		for a := range set {
			addrs = append(addrs, a)
		}
		sort.Strings(addrs)
		flat[arn] = addrs
	}
	return scanResult{unresolved: out, consumers: flat, nested: nested, nonARN: nonARN, topLevel: topLevel}, nil
}

// collectFromBodyWithHits scans every top-level attribute on a body
// for ARN literals not in the resolved set, returning the
// deduplicated-within-this-body list of hits. This is the HISTORICAL
// top-level-only pass; nested blocks and collection expressions
// (`environment { variables = {...} }`, `layers = ["arn:…"]`) are
// walked separately by scanNestedAndNonARN (finder_nested.go, class A of
// presets#834), which surfaces them as nested hits with a precise
// warning. The two passes share the same conservative "pure string
// literal only" contract (stringLiteralValue ↔ pureStringLiteral).
func collectFromBodyWithHits(body *hclwrite.Body, resolved map[string]struct{}) []string {
	var hits []string
	seen := make(map[string]struct{})
	for _, attr := range body.Attributes() {
		lit, ok := stringLiteralValue(attr)
		if !ok {
			continue
		}
		if !isARNLiteral(lit) {
			continue
		}
		if _, ok := resolved[lit]; ok {
			continue
		}
		if _, dup := seen[lit]; dup {
			continue
		}
		seen[lit] = struct{}{}
		hits = append(hits, lit)
	}
	return hits
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

// stringLiteralValue is a private copy of the helper used in
// genconfig/crossref.go. Returns the string value of an attribute iff
// it is a pure double-quoted literal — `"some-value"`. Anything more
// complex (interpolations, function calls, lists) returns ok=false so
// the caller leaves it alone.
func stringLiteralValue(attr *hclwrite.Attribute) (string, bool) {
	tokens := attr.Expr().BuildTokens(nil)
	if len(tokens) != 3 {
		return "", false
	}
	if tokens[0].Type != hclsyntax.TokenOQuote || tokens[2].Type != hclsyntax.TokenCQuote {
		return "", false
	}
	if tokens[1].Type != hclsyntax.TokenQuotedLit {
		return "", false
	}
	return string(tokens[1].Bytes), true
}
