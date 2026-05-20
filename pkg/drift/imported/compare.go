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
//
// # LabelFilter shape
//
// DriftSemanticLabelFilter is a per-key comparator: it walks the
// surviving keys of the snapshot ∪ live map after filtering out keys
// matching the policy's FieldPolicy.LabelDriftIgnorePrefixes (default
// {"goog-", "goog_"} when the policy leaves the slice empty), and
// emits ONE FieldMismatch per differing key with Field=`<path>.<key>`
// and per-key string-shaped Snapshot/Cloud. A key absent on a side
// emits an empty-string for that side. This shape mirrors the legacy
// per-type comparators in luthersystems/reliable
// (internal/agentapi/imported_drift.go's diffUserLabels) so the
// downstream UI render path is byte-identical between the curated
// upstream comparator and the per-type comparator it replaces in the
// reliable#1479 Surface B refactor.
package imported

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
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
//   - DriftSemanticLabelFilter — map-valued field compared per-key
//     after filtering out keys matching the policy's
//     LabelDriftIgnorePrefixes (default {"goog-", "goog_"} when
//     unset). One FieldMismatch per differing key, with
//     Field=`<path>.<keyname>`.
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
			out = append(out, compareLabelFilter(path, mergePrefixes(entry.LabelDriftIgnorePrefixes, entry.TagDriftIgnorePrefixes), snapAttrs, liveAttrs)...)
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

// defaultLabelDriftIgnorePrefixes is the back-compat fallback used
// when a policy declares DriftSemanticLabelFilter without setting
// LabelDriftIgnorePrefixes. Mirrors the original hardcoded set so an
// untouched policy keeps the same behavior after the per-policy
// prefix knob lands.
var defaultLabelDriftIgnorePrefixes = []string{"goog-", "goog_"}

// mergePrefixes unions a FieldPolicy's GCP-flavored
// LabelDriftIgnorePrefixes with its AWS-flavored TagDriftIgnorePrefixes.
// Both fields populate the same filter set on a DriftSemanticLabelFilter
// entry; keeping them as separate fields lets each cloud's helper
// (gcpLabelDriftPolicy / awsTagDriftPolicy) read naturally at the
// call site while the comparator stays cloud-agnostic. Order is
// label-prefixes first, then tag-prefixes; the comparator does a
// HasPrefix scan, so order only affects micro-perf, not correctness.
// nil/empty inputs are ignored; if both are empty the result is nil
// and compareLabelFilter falls back to defaultLabelDriftIgnorePrefixes.
func mergePrefixes(label, tag []string) []string {
	if len(label) == 0 {
		return tag
	}
	if len(tag) == 0 {
		return label
	}
	out := make([]string, 0, len(label)+len(tag))
	out = append(out, label...)
	out = append(out, tag...)
	return out
}

// compareLabelFilter resolves path in both maps, filters keys whose
// name has any of the ignorePrefixes on both sides, and emits ONE
// FieldMismatch per differing surviving key. The returned slice is
// unsorted — Compare sorts the merged output across all policy
// entries before returning to the caller.
//
// Field names: `<path>.<keyname>` so the UI / reason-string render
// names exactly the changed key (e.g. "labels.env" rather than the
// whole-map "labels"). Snapshot / Cloud are per-key string values:
// the JSON-decoded value coerced to its scalar string when possible
// (string kept as-is; bool/number formatted; structured values
// json.Marshal-ed) and the empty string when the key is absent on
// that side.
//
// A non-map value on either side (e.g. nil) is treated as an empty
// map for the purpose of filtering — equal empties → no mismatches.
//
// ignorePrefixes empty/nil falls back to defaultLabelDriftIgnorePrefixes
// for back-compat with policies authored before the per-policy knob.
func compareLabelFilter(path string, ignorePrefixes []string, snap, live map[string]any) []FieldMismatch {
	sv, sOK := resolvePath(path, snap)
	lv, lOK := resolvePath(path, live)
	if !sOK && !lOK {
		return nil
	}
	prefixes := ignorePrefixes
	if len(prefixes) == 0 {
		prefixes = defaultLabelDriftIgnorePrefixes
	}
	sMap := filterLabelMap(coerceMap(sv), prefixes)
	lMap := filterLabelMap(coerceMap(lv), prefixes)

	keys := make(map[string]struct{}, len(sMap)+len(lMap))
	for k := range sMap {
		keys[k] = struct{}{}
	}
	for k := range lMap {
		keys[k] = struct{}{}
	}
	if len(keys) == 0 {
		return nil
	}
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var out []FieldMismatch
	for _, k := range sortedKeys {
		sVal, sPresent := sMap[k]
		lVal, lPresent := lMap[k]
		switch {
		case !sPresent && lPresent:
			out = append(out, FieldMismatch{Field: path + "." + k, Snapshot: "", Cloud: stringifyLabelValue(lVal)})
		case sPresent && !lPresent:
			out = append(out, FieldMismatch{Field: path + "." + k, Snapshot: stringifyLabelValue(sVal), Cloud: ""})
		default:
			// Both present — emit only on inequality.
			if !reflect.DeepEqual(sVal, lVal) {
				out = append(out, FieldMismatch{Field: path + "." + k, Snapshot: stringifyLabelValue(sVal), Cloud: stringifyLabelValue(lVal)})
			}
		}
	}
	return out
}

// coerceMap returns v as map[string]any. A nil or non-map value yields
// an empty (non-nil) map so filterLabelMap can iterate uniformly.
func coerceMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// filterLabelMap returns a copy of m with any key matching one of
// ignorePrefixes removed. Prefix matching is exact-string-prefix on
// the key; an empty entry in ignorePrefixes matches every key (and is
// almost certainly a policy-author bug — the empty-fallback case is
// handled at the caller).
func filterLabelMap(m map[string]any, ignorePrefixes []string) map[string]any {
	out := make(map[string]any, len(m))
keyloop:
	for k, v := range m {
		for _, p := range ignorePrefixes {
			if strings.HasPrefix(k, p) {
				continue keyloop
			}
		}
		out[k] = v
	}
	return out
}

// stringifyLabelValue renders a per-key label value as a string, in
// the same shape the legacy reliable comparator produced (so the
// downstream UI render path stays byte-identical when reliable swaps
// to upstream Compare). The vast majority of label values are
// strings; bools/numbers/structured values are handled defensively
// so a label authored against a less-conventional schema doesn't
// surface as `<unsupported>` in the reason text.
func stringifyLabelValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s := string(b)
	// json.Marshal renders strings already-quoted; the string-case
	// above peels that off. For everything else we keep the canonical
	// JSON representation (true / 42 / [...] / {...}) so the value
	// round-trips losslessly.
	return s
}
