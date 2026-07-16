package depchase

import (
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Structured depchase warnings (presets#854).
//
// Before #854 every warning was a free-text string with a prose classification
// prefix (`nested_ref_literal:`, `non_arn_ref_literal:`, …). reverseimport
// flattened them all into job.Diagnostic{Code:"depchase_warning"}, so the prose
// prefix was a de-facto parse contract: a wording edit silently broke downstream
// classification, and seenWarning deduped on the full rendered string (a
// rendering change could duplicate a warning across iterations).
//
// Warning carries the classification as a stable Code plus the structured
// fields each class renders from. String() reproduces the historical prose
// EXACTLY (the depchase tests grep substrings; reverseimport maps Code →
// job.Diagnostic.Code with String() at the display edge). Dedup keys on
// (Code, Literal, Consumer) — the stable identity — not the rendered string.
//
// The full code list (documented in degradation_catalogue.go):
//
//	depchase_nested_ref_literal   class A — nested/collection ARN literal
//	depchase_non_arn_ref_literal  class B — curated non-ARN identifier (KMS UUID)
//	depchase_unimportable_target  class G — target inherently un-adoptable
//	depchase_unsupported_ref      class C — ARN service/rtype has no discoverer
//	depchase_unparseable_ref      class D — malformed ARN literal
//	depchase_ref_not_found        class E — DiscoverByID Err{Not,}Found
//	depchase_discoverer_rejected  class F — DiscoverByID ErrNotSupported
//	depchase_discover_error       (F1)   — non-fatal hard error on a terminal seed
//	depchase_config_omitted       class H — discovered but genconfig dropped it
//	depchase_unresolved_stable    class I — reference cycle / stable unresolved
//	depchase_reference_retained   class J — bounded by the closure contract (#864)
const (
	CodeNestedRefLiteral   = "depchase_nested_ref_literal"
	CodeNonARNRefLiteral   = "depchase_non_arn_ref_literal"
	CodeUnimportableTarget = "depchase_unimportable_target"
	CodeUnsupportedRef     = "depchase_unsupported_ref"
	CodeUnparseableRef     = "depchase_unparseable_ref"
	CodeRefNotFound        = "depchase_ref_not_found"
	CodeDiscovererRejected = "depchase_discoverer_rejected"
	CodeDiscoverError      = "depchase_discover_error"
	CodeConfigOmitted      = "depchase_config_omitted"
	CodeUnresolvedStable   = "depchase_unresolved_stable"
	// CodeReferenceRetained (class J, presets#864) marks a discovered,
	// importable dependency that the closure contract's AdoptionPolicy chose to
	// represent as a REFERENCE rather than adopt into the managed set — e.g. a
	// data-store selection referencing a foreign, out-of-scope admin IAM role.
	// The literal stays in the consumer's HCL (valid Terraform for a concrete
	// external identifier); the target is not adopted. Reason carries the
	// stable decision code (ReferenceReasonOutOfScope) so reliable's disclosure
	// surface can explain the bound without re-deriving it.
	CodeReferenceRetained = "depchase_reference_retained"
)

// Warning is a single structured depchase diagnostic. Code is the stable
// classification; the remaining fields are the render inputs (only the subset a
// given Code needs is populated). Reliable's diagnostics surface classifies on
// Code; the CLI/discover output and reverseimport Message use String().
//
// The json tags are a deliberate wire contract (presets#854): Result.Warnings
// is []Warning, so any serialization must emit stable snake_case keys rather
// than leaking Go field names. The structured shape is the point — there is no
// MarshalJSON-to-string collapse.
//
// The operator-facing noun ("ARN" vs "reference") and the seed-class word
// ("nested" vs "non-ARN") are NOT stored: they are derived in String() from the
// literal's own shape (refNoun / seedClassWord), correct by construction, so the
// emit sites carry no Label/Class bookkeeping.
type Warning struct {
	Code     string `json:"code"`     // depchase_* classification code (stable parse contract)
	Literal  string `json:"literal"`  // the reference literal (ARN / bare UUID / raw value)
	Consumer string `json:"consumer"` // consumer / omitted resource address (empty for non-attributable classes)
	Path     string `json:"path"`     // attribute path where a nested literal sits
	Attr     string `json:"attr"`     // curated attribute name (non-ARN hits)
	TFType   string `json:"tf_type"`  // parsed / resolved Terraform type
	Reason   string `json:"reason"`   // free-form detail: err text or un-importable reason code
}

// refNoun renders the operator-facing noun for a literal: an ARN-shaped literal
// is an "ARN", a bare identifier (a KMS KeyId UUID) is a "reference". Correct by
// construction from the literal's own shape — class A / top-level literals are
// gated by isARNLiteral, curated class-B literals by the UUID matcher, so the
// two vocabularies never overlap. Replaces the old per-emit-site Label field
// (G5); the derivation is byte-identical to the previous `label := "ARN"` /
// `"reference"` bookkeeping.
func refNoun(literal string) string {
	if isARNLiteral(literal) {
		return "ARN"
	}
	return "reference"
}

// seedClassWord renders the seed-class word for the non-fatal discover_error
// prose. Only terminal seeds reach that branch — a class-A nested hit (ARN) or a
// class-B nonARN hit (bare UUID) — so the word derives from the literal's shape:
// ARN ⇒ "nested", bare identifier ⇒ "non-ARN". Byte-identical to the old
// seedClass.String() value that was carried in the dropped Class field (G5).
func seedClassWord(literal string) string {
	if isARNLiteral(literal) {
		return "nested"
	}
	return "non-ARN"
}

// String renders the historical operator-facing prose for the warning, byte-
// for-byte with the pre-#854 fmt.Sprintf sites so substring-grep consumers keep
// matching.
func (w Warning) String() string {
	switch w.Code {
	case CodeNestedRefLiteral:
		return fmt.Sprintf("nested_ref_literal: reference literal inside nested attribute %s of %s; target %s — surfaced by the nested-body walk (previously silent); chasing target, nested literal retained",
			w.Path, w.Consumer, w.Literal)
	case CodeNonARNRefLiteral:
		return fmt.Sprintf("non_arn_ref_literal: non-ARN identifier in curated attribute %s of %s; value %q resolves to %s — surfaced by the curated-attr walk (previously silent); chasing by bare identifier",
			w.Attr, w.Consumer, w.Literal, w.TFType)
	case CodeUnsupportedRef:
		return fmt.Sprintf("unsupported ARN type %q (no Terraform discoverer)", w.Literal)
	case CodeUnparseableRef:
		return fmt.Sprintf("could not parse ARN %q: %s", w.Literal, w.Reason)
	case CodeRefNotFound:
		return fmt.Sprintf("%s %q (%s): %s", refNoun(w.Literal), w.Literal, w.TFType, w.Reason)
	case CodeDiscovererRejected:
		return fmt.Sprintf("%s %q: %s discoverer rejected ID: %s", refNoun(w.Literal), w.Literal, w.TFType, w.Reason)
	case CodeDiscoverError:
		return fmt.Sprintf("%s %q (%s): discovery error on a %s reference treated as non-fatal (fidelity enhancement; import preserved, literal retained): %s",
			refNoun(w.Literal), w.Literal, w.TFType, seedClassWord(w.Literal), w.Reason)
	case CodeUnimportableTarget:
		return fmt.Sprintf("%s %q (%s) references an un-importable resource (%s: %s); leaving the literal reference — the target cannot be adopted into Terraform state, so the literal is the correct terminal state",
			refNoun(w.Literal), w.Literal, w.TFType, w.Reason, imported.ReasonDescription(w.Reason))
	case CodeConfigOmitted:
		// Consumer carries the omitted (discovered) resource's address — the
		// attributable location genconfig dropped (G4). Rendered identically to
		// the pre-G4 Reason-held address.
		return fmt.Sprintf("ARN %q (%s) discovered as %s, but generated config omitted it; leaving the literal reference",
			w.Literal, w.TFType, w.Consumer)
	case CodeUnresolvedStable:
		return fmt.Sprintf("unresolved ARN reference (stable across iterations): %q", w.Literal)
	case CodeReferenceRetained:
		// The closure-contract bound (presets#864): an importable dependency
		// intentionally left as a reference instead of adopted. Consumer is the
		// referencing resource; Reason is the stable decision code.
		return fmt.Sprintf("%s %q (%s) referenced by %s left as a reference (not adopted: %s) — bounded by the reverse-import closure contract; the literal is retained and the target is not managed",
			refNoun(w.Literal), w.Literal, w.TFType, w.Consumer, w.Reason)
	default:
		// Defensive: an unmapped code still renders something traceable rather
		// than an empty string.
		return fmt.Sprintf("%s: %s", w.Code, w.Literal)
	}
}

// warningKey is the dedup identity for a Warning: (Code, Literal, Consumer).
// Distinct from the rendered string so a wording/path change can't duplicate a
// warning across iterations (presets#854). The key intentionally emits at most
// one path-representative warning per (literal, consumer) per run, even if the
// winning representative Path changes across iterations (F4/dedup).
type warningKey struct {
	code     string
	literal  string
	consumer string
}

func (w Warning) key() warningKey {
	return warningKey{code: w.Code, literal: w.Literal, consumer: w.Consumer}
}
