// Helper functions used by the per-service GCP inspectors.
//
// Filter-string helpers mirror reliable's
// internal/agentapi/gcp_filter.go (gcpLegacyLabelFilter,
// gcpLegacyLabelFilterAnd, gcpAIP160LabelFilter, gcpLabelMatches). They
// stay in the discovery package rather than the shared filter package
// because every consumer is a per-service GCP inspector — moving them
// up would force the filter package to depend on GCP wire-shape
// concerns (legacy " AND " join syntax, AIP-160 quote rules) that have
// no AWS analogue.
//
// Error helpers mirror reliable's inspect_normalize.go::
// unsupportedActionError / unsupportedServiceError, including the
// Levenshtein "did you mean?" hint (#227). The hint is consumed by the
// LLM as part of the tool-result envelope, so the format here matches
// reliable's byte-for-byte. The actual builders live at
// observability.UnsupportedActionError /
// observability.UnsupportedServiceError; these wrappers preserve the
// per-callsite signatures already in use across this package.

package gcp

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// gcpFilterValueSafe limits caller-supplied label keys/values to a
// charset that has no syntactic meaning in the GCP legacy filter
// dialect (no quote, no space, no equals, no AND/OR). Without this
// gate a value like `x AND labels.project=other` would inject an
// extra clause via fmt.Sprintf into a filter that runs against the
// caller's GCP credentials. #204 P1.
var gcpFilterValueSafe = regexp.MustCompile(`^[A-Za-z0-9._\-/]{1,128}$`)

// unsupportedActionError builds a descriptive error for an unknown
// action, listing supported actions and a Levenshtein-based "did you
// mean?" hint when one is close. Thin wrapper over
// observability.UnsupportedActionError so per-service callsites can
// keep passing the human-friendly display name (e.g. "Cloud Run") and
// their pre-fetched action list without repeating the hint logic.
func unsupportedActionError(service, action string, validActions []string) error {
	return observability.UnsupportedActionError(service, action, validActions)
}

// unsupportedServiceError builds a descriptive error for an unknown
// service, listing the canonical service registry and a "did you mean?"
// hint. Thin wrapper over observability.UnsupportedServiceError.
func unsupportedServiceError(service string, validServices []string) error {
	return observability.UnsupportedServiceError(service, validServices)
}

// parseFilterMap pulls the filter envelope into a map[string]string.
// Returns nil when filtersJSON is empty or unparseable so callers can
// keyword-check fields with normal map lookups (zero-value reads).
func parseFilterMap(filtersJSON string) map[string]string {
	if filtersJSON == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(filtersJSON), &m); err != nil {
		return nil
	}
	return m
}

// projectFromFilters extracts the "project" key from filtersJSON. Returns
// "" when not present — every per-service handler treats "" as "no
// project filter, return everything in the GCP project" (matches
// reliable's parseProjectFilter contract).
func projectFromFilters(filtersJSON string) string {
	m := parseFilterMap(filtersJSON)
	if m == nil {
		return ""
	}
	return m["project"]
}

// gcpLegacyLabelFilter returns a Compute-v1 (GCE legacy) label filter
// string of the form "labels.<key>=<value>". Returns "" for empty inputs
// so callers can pass the result directly to a request's Filter field —
// the Compute API treats "" as "no filter".
//
// The legacy dialect uses bare "=" (equality) and disallows quotes /
// spaces around the operator. ":" is substring (do NOT use for project
// scoping — "io-test" would over-include "io-test-2").
//
// Mirrors reliable's gcp_filter.go gcpLegacyLabelFilter.
//
// Caller-supplied key and value are validated against
// gcpFilterValueSafe — values that contain quote / space / "=" / " AND "
// would otherwise inject extra clauses into the filter expression
// (#204 P1). Invalid inputs return "" (no filter), which is treated by
// the SDK as "match everything"; callers that need stricter behavior
// must validate upstream.
func gcpLegacyLabelFilter(key, value string) string {
	if key == "" || value == "" {
		return ""
	}
	if !gcpFilterValueSafe.MatchString(key) || !gcpFilterValueSafe.MatchString(value) {
		return ""
	}
	return fmt.Sprintf("labels.%s=%s", key, value)
}

// gcpLegacyLabelFilterAnd joins two legacy filters with " AND ". Empty
// operands are dropped so callers can pass a base + optional addition
// without guarding the empty case. Mirrors reliable's
// gcp_filter.go gcpLegacyLabelFilterAnd.
func gcpLegacyLabelFilterAnd(a, b string) string {
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " AND " + b
	}
}

// gcpAIP160LabelFilter returns an AIP-160 label filter string of the form
// `labels.<key> = "<value>"` (note the spaces and quoting). Returns ""
// for empty inputs.
//
// AIP-160 dialect requires spaces around the operator and quotes around
// non-numeric literals. See https://google.aip.dev/160. Mirrors
// reliable's gcp_filter.go gcpAIP160LabelFilter.
//
// Caller-supplied key is validated against gcpFilterValueSafe (the
// value is %q-quoted by Sprintf and so is safe; the key is not). #204 P1.
func gcpAIP160LabelFilter(key, value string) string {
	if key == "" || value == "" {
		return ""
	}
	if !gcpFilterValueSafe.MatchString(key) {
		return ""
	}
	return fmt.Sprintf("labels.%s = %q", key, value)
}

// gcpAIP160LabelFilterAnd joins two AIP-160 filters with " AND ". Empty
// operands are dropped so callers can pass a base + optional addition
// without guarding the empty case. The AIP-160 dialect uses the same
// `AND` keyword as the legacy dialect, so the join shape is identical
// — only the per-clause format differs.
func gcpAIP160LabelFilterAnd(a, b string) string {
	switch {
	case a == "" && b == "":
		return ""
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " AND " + b
	}
}

// gcpLabelMatches reports whether labels[key] == want. An empty `want`
// is treated as match-all (caller didn't supply a project filter).
// Mirrors reliable's gcp_filter.go gcpLabelMatches. Used by post-filter
// handlers to scope returned proto slices to the session's project
// label without an SDK-level Filter call.
func gcpLabelMatches(labels map[string]string, key, want string) bool {
	if want == "" {
		return true
	}
	if labels == nil {
		return false
	}
	return labels[key] == want
}
