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
)

// Warning is a single structured depchase diagnostic. Code is the stable
// classification; the remaining fields are the render inputs (only the subset a
// given Code needs is populated). Reliable's diagnostics surface classifies on
// Code; the CLI/discover output and reverseimport Message use String().
type Warning struct {
	Code     string // depchase_* classification code (stable parse contract)
	Literal  string // the reference literal (ARN / bare UUID / raw value)
	Consumer string // consumer resource address (empty for non-attributable classes)
	Path     string // attribute path where a nested literal sits
	Attr     string // curated attribute name (non-ARN hits)
	TFType   string // parsed / resolved Terraform type
	Reason   string // free-form detail: err text, un-importable reason code, or omitted address
	Label    string // operator-facing noun in discovery prose: "ARN" or "reference"
	Class    string // seed class string, for the non-fatal discover_error prose
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
		return fmt.Sprintf("%s %q (%s): %s", w.Label, w.Literal, w.TFType, w.Reason)
	case CodeDiscovererRejected:
		return fmt.Sprintf("%s %q: %s discoverer rejected ID: %s", w.Label, w.Literal, w.TFType, w.Reason)
	case CodeDiscoverError:
		return fmt.Sprintf("%s %q (%s): discovery error on a %s reference treated as non-fatal (fidelity enhancement; import preserved, literal retained): %s",
			w.Label, w.Literal, w.TFType, w.Class, w.Reason)
	case CodeUnimportableTarget:
		return fmt.Sprintf("%s %q (%s) references an un-importable resource (%s: %s); leaving the literal reference — the target cannot be adopted into Terraform state, so the literal is the correct terminal state",
			w.Label, w.Literal, w.TFType, w.Reason, imported.ReasonDescription(w.Reason))
	case CodeConfigOmitted:
		return fmt.Sprintf("ARN %q (%s) discovered as %s, but generated config omitted it; leaving the literal reference",
			w.Literal, w.TFType, w.Reason)
	case CodeUnresolvedStable:
		return fmt.Sprintf("unresolved ARN reference (stable across iterations): %q", w.Literal)
	default:
		// Defensive: an unmapped code still renders something traceable rather
		// than an empty string.
		return fmt.Sprintf("%s: %s", w.Code, w.Literal)
	}
}

// warningKey is the dedup identity for a Warning: (Code, Literal, Consumer).
// Distinct from the rendered string so a wording/path change can't duplicate a
// warning across iterations (presets#854).
type warningKey struct {
	code     string
	literal  string
	consumer string
}

func (w Warning) key() warningKey {
	return warningKey{code: w.Code, literal: w.Literal, consumer: w.Consumer}
}
