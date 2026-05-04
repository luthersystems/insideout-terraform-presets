// Helpers used across multiple per-service files.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go +
// resource_filter.go + config_extractors.go. The pkg/observability/filter
// package owns the canonical project-tag/label matching primitives;
// helpers here are local conveniences (slice-of-maps round-trip, string
// extraction, generic Project tag predicates) that callers in the
// inspector files reuse.

package aws

import (
	"encoding/json"
	"fmt"
)

// toSliceOfMaps round-trips a typed value through JSON into the
// []map[string]any shape the filter package operates on. Used when an
// SDK response carries typed structs but the project-tag check needs the
// reflected map form (filter.Match's tagFieldName lookup works on any
// JSON-shaped record).
//
// Always returns a non-nil slice on the success path so downstream
// JSON marshaling emits `[]` not `null` (#255). AWS SDK V2 list
// responses expose empty results as typed-nil slices like
// `[]bedrockagenttypes.KnowledgeBaseSummary(nil)`, which json.Marshal
// renders as the JSON literal `null`; unmarshaling that back into
// `out` would leave it nil, so we restore an empty slice before
// returning. Returns nil only on marshal/unmarshal failure
// (fail-closed for shape mismatches).
//
// Mirrors the InsideOut backend's toSliceOfMaps (config_extractors.go:172).
func toSliceOfMaps(v any) []map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	out := []map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	if out == nil {
		return []map[string]any{}
	}
	return out
}

// toMapAny round-trips a typed value into map[string]any so the
// inspector can attach computed fields (HasInternetGateway, Kind=...) or
// pipe the record through tag-format-agnostic helpers.
//
// Mirrors the InsideOut backend's toMapAny (aws_inspect.go:1447).
func toMapAny(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// getString extracts a string field from a JSON-shaped record. Returns
// "" when the key is missing or the value is nil. Non-string values are
// fmt.Sprintf'd — preserves the InsideOut backend's permissive behaviour for SDKs
// that occasionally return numeric ARNs etc.
func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// firstNonEmptyString returns the first non-empty string from args, or
// "" if all are empty. Used for ARN extraction where a record may carry
// {KnowledgeBaseArn, AgentArn, GuardrailArn, Arn} depending on which
// AWS shape produced it.
//
// Mirrors the InsideOut backend's firstNonEmptyString (aws_inspect.go:1435).
func firstNonEmptyString(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// nilSliceToEmpty returns []T{} when s is nil so the JSON wire shape
// is `[]` not `null` (#255). AWS SDK V2 list-* responses commonly
// emit typed-nil slices on empty results — a discovery inspector that
// returns `out.SomeSliceField` directly inherits that nil and json.
// Marshal renders it as the JSON literal `null`, which the downstream
// reliable UI gates the panel render on.
//
// Wrap every direct SDK-slice passthrough at the inspector boundary:
//
//	// Bad — emits JSON null on empty:
//	return out.QueueUrls, nil
//
//	// Good:
//	return nilSliceToEmpty(out.QueueUrls), nil
//
// Loops that build a slice locally should declare it as `X := []T{}`
// at construction so the nil case never arises (the per-site fix in
// the original #255 audit). Use this helper when the loop is owned
// by the AWS SDK and you can't change the construction.
func nilSliceToEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
