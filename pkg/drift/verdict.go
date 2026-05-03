package drift

import (
	"errors"
	"fmt"
)

// ErrNoClassifiableDetail is returned by [ClassifyJSON] when the parsed
// drift carries no per-resource detail (pre-#105 schema). Callers that
// want to distinguish "input was malformed" from "input was the legacy
// schema we no longer accept" should errors.Is against this sentinel.
//
// Empty drift reports — drift_detected:false with an empty resources
// slice — do NOT trip this error: an empty Drift is classifiable as
// "no drift," which is a valid verdict.
var ErrNoClassifiableDetail = errors.New("drift: input has no classifiable per-resource detail")

// Verdict bundles a parsed [Drift] with its classifier [Result]. It is
// the canonical hand-off shape for consumers: a single value that
// answers every drift question via methods on the embedded fields.
//
// Persistence pattern: round-trip the drift.json bytes through whatever
// store fits (DB column, opaque protobuf Struct, session metadata)
// and re-derive Verdict via [ClassifyJSON] on read. The classifier is
// pure and inexpensive; storing only the input means the classifier
// version can change without invalidating cached rows.
type Verdict struct {
	// Drift is the parsed drift.json — the producer's input.
	Drift Drift
	// Result is the classifier's verdict over Drift.
	Result Result
}

// ClassifyJSON is the canonical entry point for consumers: parse
// drift.json bytes, validate that they carry per-resource detail, run
// [Classify], and return the bundled [Verdict].
//
// Error paths:
//
//   - JSON parse failure: returns a wrapped error from
//     [UnmarshalJSON]. Distinguish via errors.As against a
//     *json.SyntaxError or by checking the wrapped error's text.
//   - Missing per-resource detail: returns [ErrNoClassifiableDetail].
//     The caller has parsed JSON that decoded into a [Drift] but lacks
//     the post-#105 schema fields the rules need. Treat as an input
//     error from an out-of-date producer; do NOT silently fall back to
//     a coarser drift signal.
//   - Success: returns a non-nil *Verdict and nil error.
//
// Options are forwarded to [Classify] unchanged.
func ClassifyJSON(b []byte, opts ...Option) (*Verdict, error) {
	d, err := UnmarshalJSON(b)
	if err != nil {
		return nil, fmt.Errorf("drift: classify: %w", err)
	}
	if !HasClassifiableDetail(d) {
		return nil, ErrNoClassifiableDetail
	}
	return &Verdict{
		Drift:  d,
		Result: Classify(d, opts...),
	}, nil
}
