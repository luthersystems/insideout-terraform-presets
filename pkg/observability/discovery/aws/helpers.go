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
// JSON-shaped record). Returns nil on marshal/unmarshal failure rather
// than panicking — callers treat nil as "no records" which yields an
// empty-but-clean response.
//
// Mirrors the InsideOut backend's toSliceOfMaps (config_extractors.go:172).
func toSliceOfMaps(v any) []map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
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
