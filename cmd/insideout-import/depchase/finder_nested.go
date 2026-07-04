package depchase

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// The single dep-chase finder walk (presets#853) + nested / non-ARN reference
// detection (presets#834, classes A/B).
//
// bodyWalker performs ONE recursive hclsyntax walk of each resource body. The
// depth==0 direct attributes are the degenerate top-level case that used to be
// a separate hclwrite pass; nested blocks and collection-expression trees carry
// the two classes of cross-resource reference literal that once slipped through
// SILENTLY — no warning, no chase, quiet fidelity loss:
//
//	top-level — a pure-double-quoted ARN literal that is a direct attribute of
//	          the resource body (`role = "arn:…"`). Rewritten in place by
//	          genconfig's crossref pass, so it keeps fatal-on-discovery-error
//	          semantics; the Run loop tags it via scanResult.topLevel.
//	class A — an ARN literal nested inside a block (`environment { … }`) or a
//	          collection expression (`layers = ["arn:…"]`, `x = { k = "arn:…" }`).
//	class B — a bare, non-ARN identifier reference (a KMS KeyId UUID in
//	          `kms_key_id` / `kms_master_key_id`) that isARNLiteral never even
//	          considers.
//
// The walk surfaces all three so the chase loop can WARN on the class-A/B hits
// (Tier 1) and, where the discoverer accepts the identifier, CHASE the target
// (Tier 2). A single conservative "pure string literal only" extractor
// (pureStringLiteral) gates every position, so interpolations, traversals, and
// ARNs embedded inside larger template text stay ignored at every depth.

// uuidRe matches a canonical UUID — the shape a bare AWS KMS KeyId takes in
// `kms_key_id` / `kms_master_key_id` when the customer wrote the key id rather
// than the full key ARN. Case-insensitive: AWS accepts and the console
// sometimes renders KeyId UUIDs with uppercase hex (F9). Anchored so a UUID
// embedded in a longer string does not match.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// nonARNAttrRule maps a curated attribute name to the Terraform type its
// bare-identifier value references, plus a value matcher that gates on the
// value shape. The matcher is what keeps this a CURATED, warn-first mechanism
// rather than generic bare-name guessing: only a value that structurally looks
// like the target identifier (a UUID for a KMS key id) is surfaced, so an
// unrelated string that happens to sit in a same-named attribute never
// false-positives.
type nonARNAttrRule struct {
	tfType string
	match  func(string) bool
}

// nonARNAttrRules is the curated, extensible allowlist for class B. Extend it
// only with (attr-name → tfType) pairs whose value form is DISTINCTIVE enough
// to gate on (add a matcher), and whose tfType has a registered discoverer +
// typed model so the chased target can actually be emitted. Bare S3 bucket
// names, GCP self-links, etc. are intentionally NOT here — their value shapes
// are too permissive to match without false positives.
//
// Each entry's tfType MUST agree with the canonical cross-reference registry
// pkg/imported/dependencies (dependencies.Lookup / FieldRefs, presets#482/#667)
// — the drift test TestNonARNAttrRulesAgreeWithDependencyRegistry pins it.
//
//	Attribute            → Terraform type   (value gate)
//	kms_key_id           → aws_kms_key      (UUID)
//	kms_master_key_id    → aws_kms_key      (UUID)   (SQS/SNS CMK reference)
var nonARNAttrRules = map[string]nonARNAttrRule{
	"kms_key_id":        {tfType: "aws_kms_key", match: isKMSKeyUUID},
	"kms_master_key_id": {tfType: "aws_kms_key", match: isKMSKeyUUID},
}

// isKMSKeyUUID reports whether s is a canonical KMS KeyId UUID. Callers
// (checkNonARN) trim before invoking, so this does NOT re-trim (single TrimSpace
// at the call site — cleanup dedup).
func isKMSKeyUUID(s string) bool { return uuidRe.MatchString(s) }

// nestedHit records a class-A ARN literal found in a nested position, with the
// consumer resource address and a human-readable attribute path for the
// warning. The ARN literal itself is the map key in bodyWalker.nested, so it is
// not duplicated here (cleanup). The literal is ALSO merged into the unresolved
// set so the chase loop treats it exactly like a top-level hit (Tier 2).
type nestedHit struct {
	addr string // consumer resource address (e.g. aws_lambda_function.fn)
	path string // attribute path where the literal sits (e.g. environment.variables.KEY)
}

// nonARNHit records a class-B curated non-ARN identifier reference. The bare
// identifier value itself is the map key in bodyWalker.nonARN, so it is not
// duplicated here (cleanup).
type nonARNHit struct {
	addr   string // consumer resource address
	path   string // attribute path
	attr   string // the curated attribute name (e.g. kms_key_id)
	tfType string // resolved Terraform type (e.g. aws_kms_key)
}

// bodyWalker carries the per-resource walk state. addr is reset per resource
// block; the hit maps and the consumer-edge map accumulate across all resources.
//
// A literal that appears BOTH top-level (in X) and nested (in Y) records X in
// topLevel, records Y's nested warning + edge, and records both consumer edges
// (F4). The unified consumers keyspace dedups the CHASE to one call; the
// top-level-vs-nested class distinction is made by the Run loop from
// scanResult.topLevel.
type bodyWalker struct {
	addr     string
	resolved map[string]struct{}
	// topLevel is the set of ARN literals found as depth-0 direct attributes —
	// the historical top-level pass's job, now folded into this single walk.
	topLevel map[string]struct{}
	nested   map[string]nestedHit
	nonARN   map[string]nonARNHit
	// consumers records EVERY distinct (literal → consumer addr) pair the walk
	// observes (F4). The nested / nonARN winner-hit maps keep only one
	// representative (addr, path) per literal for the warning text; consumers
	// keeps them all so no consumer's dependency edge is dropped when a literal
	// is referenced by more than one resource (or also appears top-level).
	consumers map[string]map[string]struct{}
}

// addConsumer records one (literal → consumer addr) edge candidate.
func (w *bodyWalker) addConsumer(lit, addr string) {
	set, ok := w.consumers[lit]
	if !ok {
		set = map[string]struct{}{}
		w.consumers[lit] = set
	}
	set[addr] = struct{}{}
}

// walkBody recurses one body. depth is 0 for the resource's own top-level
// attributes — whose pure-literal ARN values are the historical top-level case
// (recorded via recordTopLevelARN) — and increments once per nested block, so
// depth>0 makes a pure-literal ARN a class-A hit (recordNestedARN). Class B is
// attr-name gated and applies at every depth.
func (w *bodyWalker) walkBody(body *hclsyntax.Body, prefix string, depth int) {
	for name, attr := range body.Attributes {
		path := joinPath(prefix, name)
		if v, ok := pureStringLiteral(attr.Expr); ok {
			// class B is attr-name gated and applies at any depth where an
			// attribute name exists (top-level attrs + nested-block attrs).
			w.checkNonARN(name, v, path)
			if isARNLiteral(v) {
				if depth == 0 {
					// Degenerate top-level case: a direct attribute of the
					// resource body. crossref rewrites it, so it keeps
					// top-level chase semantics.
					w.recordTopLevelARN(v)
				} else {
					w.recordNestedARN(v, path)
				}
			}
			continue
		}
		// A complex expression (list / object / …): descend into its element
		// tree collecting pure-literal ARNs, which are nested by construction.
		w.walkExprARNs(attr.Expr, path)
	}
	for _, sub := range body.Blocks {
		// Include block labels in the path segment so two same-type labeled
		// blocks (e.g. `rule "a" {}` / `rule "b" {}`) produce distinct paths
		// in the emitted warnings (F10). Rendered as `type[label][label…]`.
		seg := sub.Type
		for _, lbl := range sub.Labels {
			seg += "[" + lbl + "]"
		}
		w.walkBody(sub.Body, joinPath(prefix, seg), depth+1)
	}
}

// walkExprARNs collects pure-literal ARN values (class A) and curated non-ARN
// identifiers (class B) from a tuple/object expression tree. Function calls,
// traversals, and other node kinds are deliberately not descended — the
// conservative contract only acts on concrete string literals. Every literal
// reached through a collection expression is nested by construction, so ARN
// hits here are always class A regardless of the enclosing depth.
func (w *bodyWalker) walkExprARNs(expr hclsyntax.Expression, path string) {
	switch e := expr.(type) {
	case *hclsyntax.TupleConsExpr:
		for i, el := range e.Exprs {
			w.walkExprARNs(el, fmt.Sprintf("%s[%d]", path, i))
		}
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			key := objectKey(item.KeyExpr)
			itemPath := joinPath(path, key)
			// A pure-literal object VALUE is a leaf: check it for both classes
			// here (F6 — class B in object expressions was previously only
			// checked on body attributes, never on object items). Non-literal
			// values recurse.
			if v, ok := pureStringLiteral(item.ValueExpr); ok {
				w.checkNonARN(key, v, itemPath)
				if isARNLiteral(v) {
					w.recordNestedARN(v, itemPath)
				}
				continue
			}
			w.walkExprARNs(item.ValueExpr, itemPath)
		}
	default:
		if v, ok := pureStringLiteral(expr); ok && isARNLiteral(v) {
			w.recordNestedARN(v, path)
		}
	}
}

// recordTopLevelARN stores a depth-0 direct-attribute ARN hit, skipping
// literals that are in-batch (resolved). Top-level hits are a set (the Run loop
// only needs "is this literal top-level?" for origin-class tagging), so no
// (addr, path) representative is tracked here — the consumer edge carries the
// address. The literal is recorded UNCONDITIONALLY (minus resolved) so a
// top-level occurrence's edge survives even when the same ARN also appears
// nested elsewhere (F4).
func (w *bodyWalker) recordTopLevelARN(arn string) {
	if _, ok := w.resolved[arn]; ok {
		return
	}
	w.topLevel[arn] = struct{}{}
	w.addConsumer(arn, w.addr)
}

// recordNestedARN stores a class-A hit, skipping literals that are in-batch
// (resolved). The consumer edge is recorded UNCONDITIONALLY (minus resolved):
// even when the same ARN also appears top-level in another resource, this
// nested consumer's dependency edge must survive (F4). When the same ARN
// appears in multiple nested positions the lexicographically-smallest
// (addr, path) wins so the emitted warning is byte-stable across runs despite
// non-deterministic map iteration.
func (w *bodyWalker) recordNestedARN(arn, path string) {
	if _, ok := w.resolved[arn]; ok {
		return
	}
	w.addConsumer(arn, w.addr)
	cand := nestedHit{addr: w.addr, path: path}
	if existing, ok := w.nested[arn]; !ok || lessLoc(cand.addr, cand.path, existing.addr, existing.path) {
		w.nested[arn] = cand
	}
}

// checkNonARN stores a class-B hit when the attribute name is curated and its
// value passes the type's shape gate and is not already in-batch. The consumer
// edge is recorded for every consumer of the identifier (F4).
func (w *bodyWalker) checkNonARN(name, value, path string) {
	rule, ok := nonARNAttrRules[name]
	if !ok {
		return
	}
	v := strings.TrimSpace(value)
	if !rule.match(v) {
		return
	}
	if _, ok := w.resolved[v]; ok {
		return
	}
	w.addConsumer(v, w.addr)
	cand := nonARNHit{addr: w.addr, path: path, attr: name, tfType: rule.tfType}
	if existing, ok := w.nonARN[v]; !ok || lessLoc(cand.addr, cand.path, existing.addr, existing.path) {
		w.nonARN[v] = cand
	}
}

// pureStringLiteral is the single "pure string literal" extractor for the
// finder (presets#853 collapsed the historical hclwrite token-count copy into
// this typed-AST implementation). It returns the value of an expression IFF the
// expression is a pure string literal with no interpolation. A template with
// any `${…}` part, a traversal, or a non-string literal returns ok=false so the
// walk leaves it alone — the conservative contract enforced identically at
// every depth.
func pureStringLiteral(expr hclsyntax.Expression) (string, bool) {
	switch e := expr.(type) {
	case *hclsyntax.TemplateExpr:
		if !e.IsStringLiteral() {
			return "", false
		}
		v, diags := e.Value(nil)
		if diags.HasErrors() || v.IsNull() || v.Type() != cty.String {
			return "", false
		}
		return v.AsString(), true
	case *hclsyntax.LiteralValueExpr:
		if e.Val.IsNull() || e.Val.Type() != cty.String {
			return "", false
		}
		return e.Val.AsString(), true
	}
	return "", false
}

// objectKey renders an object item's key for the attribute path. Handles both
// quoted keys (`"foo" = …`) and bare-identifier keys (`foo = …`); anything
// exotic — including a parenthesized dynamic key `(expr) = …` — collapses to
// "*".
//
// This mirrors pkg/composer.objectConsKeyAsString (the canonical helper). In
// particular it re-applies that helper's ForceNonLiteral guard: HCL sets
// ForceNonLiteral on the ObjectConsKeyExpr when the source wrapped the key in
// parens to force dynamic evaluation, and treating such a key as a static name
// would fabricate a bogus path segment. (Kept as a local copy rather than
// importing the unexported composer helper; keep the two in sync.)
func objectKey(e hclsyntax.Expression) string {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		// Parenthesized `(expr) = …` dynamic key: not a static name.
		if k.ForceNonLiteral {
			return "*"
		}
		e = k.Wrapped
	}
	if v, ok := pureStringLiteral(e); ok {
		return v
	}
	if t, ok := e.(*hclsyntax.ScopeTraversalExpr); ok && len(t.Traversal) == 1 {
		return t.Traversal.RootName()
	}
	return "*"
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// lessLoc orders two (addr, path) locations for deterministic hit selection.
func lessLoc(a1, p1, a2, p2 string) bool {
	if a1 != a2 {
		return a1 < a2
	}
	return p1 < p2
}
