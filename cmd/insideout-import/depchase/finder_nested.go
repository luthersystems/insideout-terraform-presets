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
// duplicated here (cleanup). No attribute path is kept: the class-B warning
// renders from attr (the curated attribute name), never a path, so tracking one
// was dead weight (G5). Deterministic winner selection tie-breaks on (addr,
// attr).
type nonARNHit struct {
	addr   string // consumer resource address
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
	addr string
	// src is the raw generated.tf bytes, so a depth-0 hit can compare the
	// expression's raw source against the strict `"<value>"` shape (G1).
	src      []byte
	resolved map[string]struct{}
	// skip is the caller-supplied terminal/ignored literal set (ignoredARNs ∪
	// chased). Treated exactly like resolved: a literal in it is not re-recorded,
	// so the Run loop no longer re-records-then-re-filters a terminal literal
	// every iteration (G5). Empty on iteration 1, so first-pass edges/warnings
	// are unchanged.
	skip map[string]struct{}
	// topLevel is the set of ARN literals found as depth-0 direct attributes
	// whose raw source is a strict, crossref-rewritable quoted literal — the
	// historical top-level pass's job, now folded into this single walk.
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
//
// INVARIANT: a topLevel hit ⇒ a crossref-rewritable strict quoted literal. The
// depth-0 branch records topLevel ONLY when the expression's raw source is the
// plain `"<value>"` shape (strictQuotedLiteral) — genconfig's crossref pass, and
// the resolved-set byte-match, both act only on that shape. A depth-0 ARN that
// pureStringLiteral accepts but crossref cannot (a heredoc, or an
// escape-decoded literal) is recorded through the NESTED path instead: it is
// terminal-by-design, exactly like a genuinely nested literal, because crossref
// will never rewrite it and it can never byte-match the resolved set (G1). The
// deleted hclwrite `stringLiteralValue` enforced this implicitly via its strict
// OQuote/QuotedLit/CQuote 3-token contract; e.g. `role = <<EOT\narn:…\nEOT`.
//
// Path building is lazy: it is materialized only in the branches that consume it
// (a nested-ARN record, or the collection-expr recursion). A strict depth-0
// top-level hit and a class-B check need no path, so a body of plain scalar
// attributes builds none (G5).
func (w *bodyWalker) walkBody(body *hclsyntax.Body, prefix string, depth int) {
	for name, attr := range body.Attributes {
		if v, ok := pureStringLiteral(attr.Expr); ok {
			// class B is attr-name gated and applies at any depth where an
			// attribute name exists (top-level attrs + nested-block attrs).
			w.checkNonARN(name, v)
			if isARNLiteral(v) {
				if depth == 0 && strictQuotedLiteral(w.src, attr.Expr, v) {
					// Degenerate top-level case: a direct attribute of the
					// resource body whose raw source is a strict, rewritable
					// quoted literal. crossref rewrites it, so it keeps
					// top-level chase semantics.
					w.recordTopLevelARN(v)
				} else {
					// depth>0, OR a depth-0 heredoc / escape-bearing literal that
					// crossref can never rewrite: record through the nested,
					// terminal-by-design path, with the attribute name as the
					// path (G1).
					w.recordNestedARN(v, joinPath(prefix, name))
				}
			}
			continue
		}
		// A complex expression (list / object / …): descend into its element
		// tree collecting pure-literal ARNs, which are nested by construction.
		w.walkExprARNs(attr.Expr, joinPath(prefix, name))
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
				w.checkNonARN(key, v)
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

// skipped reports whether a literal must not be recorded because it is already
// in-batch (resolved) OR in the caller's terminal/ignored skip-set (G5).
func (w *bodyWalker) skipped(lit string) bool {
	if _, ok := w.resolved[lit]; ok {
		return true
	}
	_, ok := w.skip[lit]
	return ok
}

// recordTopLevelARN stores a depth-0 direct-attribute ARN hit, skipping literals
// that are in-batch (resolved) or already terminal/ignored (skip). Top-level
// hits are a set (the Run loop only needs "is this literal top-level?" for
// origin-class tagging), so no (addr, path) representative is tracked here — the
// consumer edge carries the address. The literal is recorded UNCONDITIONALLY
// (minus skipped) so a top-level occurrence's edge survives even when the same
// ARN also appears nested elsewhere (F4). The value is TrimSpace'd to mirror the
// class-B / nested record sites, so the recorded key matches the trimmed form
// isARNLiteral gated on (G2) — a no-op for a genuine strict quoted literal.
func (w *bodyWalker) recordTopLevelARN(arn string) {
	arn = strings.TrimSpace(arn)
	if w.skipped(arn) {
		return
	}
	w.topLevel[arn] = struct{}{}
	w.addConsumer(arn, w.addr)
}

// recordNestedARN stores a class-A hit, skipping literals that are in-batch
// (resolved) or terminal/ignored (skip). The consumer edge is recorded
// UNCONDITIONALLY (minus skipped): even when the same ARN also appears top-level
// in another resource, this nested consumer's dependency edge must survive (F4).
// The ARN is TrimSpace'd FIRST (G2): recordNestedARN now receives depth-0
// heredoc / escape-bearing literals (G1), whose extracted value can carry a
// trailing newline; storing the raw value would key the map and Warning.Literal
// on whitespace that can never byte-match the resolved set. When the same ARN
// appears in multiple nested positions the lexicographically-smallest
// (addr, path) wins so the emitted warning is byte-stable across runs despite
// non-deterministic map iteration.
func (w *bodyWalker) recordNestedARN(arn, path string) {
	arn = strings.TrimSpace(arn)
	if w.skipped(arn) {
		return
	}
	w.addConsumer(arn, w.addr)
	cand := nestedHit{addr: w.addr, path: path}
	if existing, ok := w.nested[arn]; !ok || lessLoc(cand.addr, cand.path, existing.addr, existing.path) {
		w.nested[arn] = cand
	}
}

// checkNonARN stores a class-B hit when the attribute name is curated and its
// value passes the type's shape gate and is not already in-batch / skipped. The
// consumer edge is recorded for every consumer of the identifier (F4). The
// deterministic winner tie-breaks on (addr, attr) — no path is kept (G5).
func (w *bodyWalker) checkNonARN(name, value string) {
	rule, ok := nonARNAttrRules[name]
	if !ok {
		return
	}
	v := strings.TrimSpace(value)
	if !rule.match(v) {
		return
	}
	if w.skipped(v) {
		return
	}
	w.addConsumer(v, w.addr)
	cand := nonARNHit{addr: w.addr, attr: name, tfType: rule.tfType}
	if existing, ok := w.nonARN[v]; !ok || lessLoc(cand.addr, cand.attr, existing.addr, existing.attr) {
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

// strictQuotedLiteral reports whether expr's RAW source is exactly a plain
// double-quoted literal `"<value>"` — no heredoc, no escape sequences, no
// interpolation. This is the ONLY shape genconfig's crossref pass can rewrite in
// place (it uses a strict OQuote/QuotedLit/CQuote token contract), and the ONLY
// shape whose recorded value can byte-match buildResolvedSet. A depth-0 ARN is
// recorded top-level ONLY when this holds (G1); everything else (heredocs,
// escape-decoded literals) flows through the terminal-by-design nested path,
// because pureStringLiteral's escape/heredoc decoding can accept literals the
// old hclwrite stringLiteralValue rejected. Compared via the expression's byte
// Range, so no re-lexing.
func strictQuotedLiteral(src []byte, expr hclsyntax.Expression, value string) bool {
	rng := expr.Range()
	if rng.Start.Byte < 0 || rng.End.Byte > len(src) || rng.Start.Byte >= rng.End.Byte {
		return false
	}
	raw := src[rng.Start.Byte:rng.End.Byte]
	// Exactly `"` + value + `"`: same length (so no escape shortened/lengthened
	// the decoded value), quote-delimited, inner bytes byte-equal to value.
	return len(raw) == len(value)+2 &&
		raw[0] == '"' && raw[len(raw)-1] == '"' &&
		string(raw[1:len(raw)-1]) == value
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
