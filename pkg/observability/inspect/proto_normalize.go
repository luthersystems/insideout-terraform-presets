// protoNormalize converts a result that may contain protobuf-generated
// messages into a JSON-friendly shape that uses **lowerCamelCase**
// field names and **named enum strings** — i.e. the shape `protojson`
// produces.
//
// Why this exists:
//
// GCP inspectors return typed proto pointers (e.g. []*pubsubpb.Topic,
// []*containerpb.Cluster). When these flow through Go's standard
// encoding/json the JSON keys come out in **snake_case** (proto-gen
// struct tags) and enum values come out as **integers**:
//
//	{"name":"...", "current_node_count":3, "status":2,
//	 "message_retention_duration":{"seconds":604800}}
//
// But the live-config extractors and the LLM summarizers all read
// lowerCamelCase keys and expect named enum strings (matching the
// Discovery / protojson convention). Without this normalization, every
// proto-based GCP component returns mostly-empty config / summary maps
// in production — the keys read by the consumer don't match the keys
// produced by the marshaller.
//
// protoNormalize fixes that at the inspector boundary: every caller of
// the GCP dispatcher (HTTP handler, batch handler, component-metrics,
// LLM tool) gets the protojson shape. AWS results are pass-through
// (none of them are proto.Message), so the AWS code path is unaffected.
//
// The function is shape-preserving in three ways:
//
//  1. A slice of proto.Message → []any of map[string]any (one per
//     element).
//  2. A single proto.Message → map[string]any.
//  3. A map containing nested proto.Message values → recurse and
//     normalize each value.
//
// Anything else (already-built map[string]any, []string, plain structs
// from Discovery SDKs, primitives) passes through unchanged.
//
// Lifted verbatim from
// reliable/internal/agentapi/proto_normalize.go.
package inspect

import (
	"encoding/json"
	"reflect"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func protoNormalize(v any) any {
	if v == nil {
		return nil
	}
	// Single proto.Message — most common single-result shape (e.g.
	// billing-info, describe-* GET calls).
	if msg, ok := v.(proto.Message); ok {
		if m, ok := protoMessageToMap(msg); ok {
			return m
		}
		return v
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice:
		// Slice of proto.Message — the common list-* result shape.
		if normalized, ok := normalizeProtoSlice(rv); ok {
			return normalized
		}
		// Slice of something non-proto — pass through.
		return v
	case reflect.Map:
		// Recurse into map values so nested proto messages get
		// normalized (identity_platform list-providers case).
		if mv, ok := v.(map[string]any); ok {
			out := make(map[string]any, len(mv))
			for k, val := range mv {
				out[k] = protoNormalize(val)
			}
			return out
		}
		return v
	default:
		return v
	}
}

// normalizeProtoSlice handles the slice-of-proto case used by every
// GCP list-* inspector. Returns ([]any of map[string]any, true) on
// success; (nil, false) when the slice is not a proto-message slice
// (caller should pass through).
//
// Empty slice is treated as "not a proto slice" — there is nothing to
// probe and the caller's downstream JSON encoder produces the same
// empty array either way.
//
// Two slice shapes are handled:
//
//  1. Typed slices ([]*pubsubpb.Topic, etc.) — Go's type system
//     enforces homogeneity, so probing the first element's
//     proto.Message-ness tells us about every element.
//  2. Interface slices ([]any) — could be mixed. We don't see this in
//     production today, but the function's "shape preservation on
//     failure" contract requires checking each element. If ANY element
//     isn't a proto, bail and pass through unchanged.
func normalizeProtoSlice(rv reflect.Value) (any, bool) {
	if rv.Len() == 0 {
		return nil, false
	}
	// Fast path: typed slice. The element type tells us up front
	// whether proto.Message is implemented, so a single first-element
	// probe is sufficient to decide for the whole slice.
	if rv.Type().Elem().Kind() != reflect.Interface {
		first := rv.Index(0).Interface()
		if _, ok := first.(proto.Message); !ok {
			return nil, false
		}
		return marshalProtoElements(rv)
	}
	// Slow path: interface slice. Check every element before
	// committing, so a heterogeneous `[]any{stubMap, *pubsubpb.Topic{...}}`
	// falls through cleanly instead of getting half-normalized.
	for i := 0; i < rv.Len(); i++ {
		if _, ok := rv.Index(i).Interface().(proto.Message); !ok {
			return nil, false
		}
	}
	return marshalProtoElements(rv)
}

// marshalProtoElements converts every element of rv (already verified
// to implement proto.Message) into a map[string]any via protojson.
func marshalProtoElements(rv reflect.Value) (any, bool) {
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		msg, ok := rv.Index(i).Interface().(proto.Message)
		if !ok {
			return nil, false
		}
		m, ok := protoMessageToMap(msg)
		if !ok {
			return nil, false
		}
		out[i] = m
	}
	return out, true
}

// protoMessageToMap is the per-element conversion: protojson.Marshal
// then json.Unmarshal into a generic map. Failures (which would only
// happen if the proto message were corrupted) return (nil, false) so
// the caller can leave the original value alone.
func protoMessageToMap(msg proto.Message) (map[string]any, bool) {
	b, err := protojson.Marshal(msg)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return m, true
}
