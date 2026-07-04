package depchase

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Nested + non-ARN reference detection (presets#834, classes A/B).
//
// The historical finder (finder.go:collectFromBodyWithHits) scanned ONLY
// top-level, pure-double-quoted attribute values that were ARN-shaped. Two
// whole classes of cross-resource reference literal slipped through SILENTLY —
// no warning, no chase, quiet fidelity loss:
//
//	class A — an ARN literal nested inside a block (`environment { … }`) or a
//	          collection expression (`layers = ["arn:…"]`, `x = { k = "arn:…" }`).
//	class B — a bare, non-ARN identifier reference (a KMS KeyId UUID in
//	          `kms_key_id` / `kms_master_key_id`) that isARNLiteral never even
//	          considers.
//
// scanNestedAndNonARN walks each resource body recursively (nested blocks +
// tuple/object expression trees) with the hclsyntax typed AST and surfaces both
// classes so the chase loop can WARN on them (Tier 1) and, where the discoverer
// accepts the identifier, CHASE the target (Tier 2). It deliberately reuses the
// same conservative "pure string literal only" contract as the top-level pass
// (pureStringLiteral below mirrors stringLiteralValue) so interpolations,
// traversals, and ARNs embedded inside larger template text stay ignored.

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

// scanNestedAndNonARN walks every resource body's nested blocks and
// collection-expression trees, returning the class-A nested ARN hits, the
// class-B curated non-ARN hits, and the full set of (literal → consumer
// address) pairs for both classes. resolved is the in-batch identity set — a
// nested/non-ARN literal that points at an already-imported resource is not
// unresolved.
//
// Unlike an earlier revision, the walk no longer subtracts the top-level ARN
// set: a literal that appears BOTH top-level (in X) and nested (in Y) must
// STILL record Y's consumer edge and STILL surface Y's nested warning (F4). The
// unified `seen` keyspace in scanGenerated dedups the CHASE to one call; the
// top-level-vs-nested class distinction is made by the Run loop from
// scanResult.topLevel, not here.
func scanNestedAndNonARN(raw []byte, resolved map[string]struct{}) (nested map[string]nestedHit, nonARN map[string]nonARNHit, consumers map[string]map[string]struct{}, err error) {
	file, diags := hclsyntax.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, nil, nil, fmt.Errorf("depchase: parse generated.tf (syntax): %s", diags.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, nil, nil, fmt.Errorf("depchase: unexpected body type %T", file.Body)
	}
	w := &bodyWalker{
		resolved:  resolved,
		nested:    map[string]nestedHit{},
		nonARN:    map[string]nonARNHit{},
		consumers: map[string]map[string]struct{}{},
	}
	for _, blk := range body.Blocks {
		if blk.Type != "resource" || len(blk.Labels) != 2 {
			continue
		}
		w.addr = blk.Labels[0] + "." + blk.Labels[1]
		w.walkBody(blk.Body, "", false)
	}
	return w.nested, w.nonARN, w.consumers, nil
}

// bodyWalker carries the per-resource walk state. addr is reset per resource
// block; the hit maps and the consumer-edge map accumulate across all resources.
type bodyWalker struct {
	addr     string
	resolved map[string]struct{}
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

// walkBody recurses one body. inNested is false for the resource's own
// top-level attributes (whose pure-literal ARN values are the historical
// pass's job) and true once we descend into a nested block or collection —
// only then is a pure-literal ARN a class-A hit.
func (w *bodyWalker) walkBody(body *hclsyntax.Body, prefix string, inNested bool) {
	for name, attr := range body.Attributes {
		path := joinPath(prefix, name)
		if v, ok := pureStringLiteral(attr.Expr); ok {
			// class B is attr-name gated and applies at any depth where an
			// attribute name exists (top-level attrs + nested-block attrs).
			w.checkNonARN(name, v, path)
			if inNested && isARNLiteral(v) {
				w.recordNestedARN(v, path)
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
		w.walkBody(sub.Body, joinPath(prefix, seg), true)
	}
}

// walkExprARNs collects pure-literal ARN values (class A) and curated non-ARN
// identifiers (class B) from a tuple/object expression tree. Function calls,
// traversals, and other node kinds are deliberately not descended — the
// conservative contract only acts on concrete string literals.
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

// pureStringLiteral is the hclsyntax analogue of stringLiteralValue: it returns
// the value of an expression IFF the expression is a pure string literal with
// no interpolation. A template with any `${…}` part, a traversal, or a
// non-string literal returns ok=false so the walk leaves it alone — matching
// the top-level pass's conservative contract exactly.
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
