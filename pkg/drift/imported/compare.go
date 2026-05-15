// Package imported is the generic curated-field drift comparator for
// sealed snapshot vs. live cloud read. It is the engine behind the
// eventual pkg/imported.Provider.CompareDrift method (issue #482).
//
// # Why this lives here
//
// The comparator is policy-driven: per-cloud presets declare a
// FieldPolicy map in pkg/composer/imported/policy, and each curated
// entry carries a DriftSemantic axis (Exact / WholeList / LabelFilter /
// None — see policy/axes.go). Compare walks the registered policy for
// a Terraform type and dispatches on DriftSemantic to decide whether a
// per-field value diverged between snapshot and live, returning a
// deterministic []FieldMismatch.
//
// Compare deliberately treats both inputs as best-effort observational
// data — malformed JSON, missing fields, and unregistered types all
// resolve to nil rather than an error. Drift reporting is a downstream
// signal, never an apply gate.
//
// # Path resolution
//
// FieldPolicy keys use dotted-path syntax matching
// pkg/composer/imported/policy/path.go's grammar (without bracket
// suffixes — Compare does not interpret list indices). At each
// segment the resolver:
//
//   - takes the map key when the current node is map[string]any
//   - auto-unwraps a singleton list (one-element []any) — Terraform's
//     block-shaped attributes serialize as [{...}] in state JSON, and
//     the policy paths are authored against the logical flat shape
//     (e.g. "versioning.enabled" — see google_storage_bucket.policy.go).
//   - stops with a missing-value signal otherwise.
//
// When both sides of the compare resolve identically (including both
// absent → both nil), no mismatch is reported.
package imported

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// FieldMismatch is a type alias for pkg/imported.FieldMismatch so
// Compare's output drops straight into Provider.CompareDrift without
// a per-element conversion at the call site. The alias direction
// (drift → imported) is safe: pkg/imported does not import this
// package, only the per-cloud Provider impls do.
type FieldMismatch = imp.FieldMismatch

// Compare reports the curated-field drift between a sealed snapshot
// and a fresh live read for tfType. Dispatch is driven by the
// FieldPolicy.DriftSemantic axis on each policy entry:
//
//   - DriftSemanticNone — field skipped (default for uncurated
//     fields).
//   - DriftSemanticExact — exact equality between snapshot[path] and
//     live[path], via reflect.DeepEqual.
//   - DriftSemanticWholeList — list-valued field compared as a whole
//     (order-sensitive), via reflect.DeepEqual after coercion to
//     []any.
//   - DriftSemanticLabelFilter — map-valued field compared after
//     filtering out keys with a "goog-" or "goog_" prefix (the only
//     auto-populated label namespace today). Per-policy filter prefix
//     support is documented in axes.go and is a follow-up; the
//     current behavior is fixed to the GCP label-noise case.
//
// Types without a registered policy return nil (no fields curated →
// no drift signal). Malformed JSON in either Attrs returns nil — this
// is observability, not validation.
//
// Output is sorted by Field for deterministic golden tests.
func Compare(tfType string, snapshot, live json.RawMessage) []FieldMismatch {
	policyMap, ok := policy.Lookup(tfType)
	if !ok {
		return nil
	}
	snapAttrs, ok := decodeAttrs(snapshot)
	if !ok {
		return nil
	}
	liveAttrs, ok := decodeAttrs(live)
	if !ok {
		return nil
	}
	var out []FieldMismatch
	for path, entry := range policyMap {
		switch entry.DriftSemantic {
		case policy.DriftSemanticNone:
			continue
		case policy.DriftSemanticExact:
			if m, ok := compareExact(path, snapAttrs, liveAttrs); ok {
				out = append(out, m)
			}
		case policy.DriftSemanticWholeList:
			if m, ok := compareWholeList(path, snapAttrs, liveAttrs); ok {
				out = append(out, m)
			}
		case policy.DriftSemanticLabelFilter:
			if m, ok := compareLabelFilter(path, snapAttrs, liveAttrs); ok {
				out = append(out, m)
			}
		default:
			// Unknown DriftSemantic — be conservative and skip. The
			// policy lint enforces Valid() at registration, so an
			// unknown value reaching here means a runtime hand-built
			// map; emitting noise would be worse than silence.
			continue
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// decodeAttrs parses a json.RawMessage into a map[string]any. A nil
// or empty input decodes to an empty map (treated as "no attrs",
// still compareable). Returns ok=false only on a parse error or on a
// non-object top level, which propagates as "no drift signal" per
// the best-effort contract.
func decodeAttrs(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		return map[string]any{}, true
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	if m == nil {
		return map[string]any{}, true
	}
	return m, true
}

// resolvePath walks dotted-path segments into a map[string]any,
// auto-unwrapping single-element list nodes (terraform's
// block-as-singleton-list state shape). Returns (value, true) when
// the full path resolves, or (nil, false) when any segment misses.
func resolvePath(path string, m map[string]any) (any, bool) {
	if path == "" {
		return nil, false
	}
	segs := strings.Split(path, ".")
	var cur any = m
	for _, seg := range segs {
		// Auto-unwrap a singleton list — Terraform serializes block
		// fields as [{...}] in state; policy paths are authored
		// against the logical flat object.
		if lst, ok := cur.([]any); ok {
			if len(lst) != 1 {
				return nil, false
			}
			cur = lst[0]
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		next, present := obj[seg]
		if !present {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// compareExact resolves path in both maps and emits a mismatch iff
// the values diverge. Both-absent / equal-values → no mismatch.
func compareExact(path string, snap, live map[string]any) (FieldMismatch, bool) {
	sv, sOK := resolvePath(path, snap)
	lv, lOK := resolvePath(path, live)
	if !sOK && !lOK {
		return FieldMismatch{}, false
	}
	if sOK && lOK && reflect.DeepEqual(sv, lv) {
		return FieldMismatch{}, false
	}
	return FieldMismatch{Field: path, Snapshot: sv, Cloud: lv}, true
}

// compareWholeList resolves path in both maps and compares as lists.
// Both sides are coerced to []any first; a non-list value (or
// absence) on one side and a list on the other → mismatch.
func compareWholeList(path string, snap, live map[string]any) (FieldMismatch, bool) {
	sv, sOK := resolvePath(path, snap)
	lv, lOK := resolvePath(path, live)
	if !sOK && !lOK {
		return FieldMismatch{}, false
	}
	sList := coerceList(sv)
	lList := coerceList(lv)
	// Treat absence as nil-list; equal nil-lists → no mismatch.
	if reflect.DeepEqual(sList, lList) {
		return FieldMismatch{}, false
	}
	return FieldMismatch{Field: path, Snapshot: sList, Cloud: lList}, true
}

// coerceList returns v as []any. A nil or non-list value yields nil
// (which DeepEqual compares as equal to other nil []any).
func coerceList(v any) []any {
	if v == nil {
		return nil
	}
	if lst, ok := v.([]any); ok {
		return lst
	}
	// Defensive: wrap a single object as a 1-element list so the
	// compare degrades to "we have something vs you have something"
	// rather than spuriously matching the nil-list branch.
	return []any{v}
}

// compareLabelFilter resolves path in both maps, filters auto-populated
// goog-* / goog_* keys on both sides, and compares the residue. A
// non-map value on either side (e.g. nil) is treated as an empty map
// for the purpose of filtering — equal empties → no mismatch.
func compareLabelFilter(path string, snap, live map[string]any) (FieldMismatch, bool) {
	sv, sOK := resolvePath(path, snap)
	lv, lOK := resolvePath(path, live)
	if !sOK && !lOK {
		return FieldMismatch{}, false
	}
	sMap := filterAutoLabels(coerceMap(sv))
	lMap := filterAutoLabels(coerceMap(lv))
	if reflect.DeepEqual(sMap, lMap) {
		return FieldMismatch{}, false
	}
	return FieldMismatch{Field: path, Snapshot: sMap, Cloud: lMap}, true
}

// coerceMap returns v as map[string]any. A nil or non-map value yields
// an empty (non-nil) map so filterAutoLabels can iterate uniformly.
func coerceMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// filterAutoLabels returns a copy of m with any "goog-" / "goog_"
// prefixed keys removed. These are GCP's auto-populated label
// namespace (e.g. goog-managed-by, goog_terraform_provisioned) and
// drift on them is provider noise, not user-actionable.
//
// Future: per-policy filter-prefix support is documented in
// axes.go's DriftSemanticLabelFilter comment. When that lands the
// caller will pass the prefix; for now the rule is fixed to the
// goog-* namespace.
func filterAutoLabels(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
			continue
		}
		out[k] = v
	}
	return out
}
