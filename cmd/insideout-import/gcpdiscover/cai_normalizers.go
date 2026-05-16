package gcpdiscover

// cai_normalizers.go — composable per-type Normalizer helpers for the
// Cloud Asset Inventory unified enricher (#510, mirror of the AWS
// Cloud Control #501 pattern in cloudcontrol_normalizers.go).
//
// A Normalizer transforms the raw CAI versionedResources JSON before
// the generic camelToSnakeGCP / Layer-1 unmarshal pipeline in
// cloudAssetEnricher.fetchAndMap. The GCP REST representation and the
// Terraform attribute schema diverge in a small set of mechanical ways
// that the snake_case renamer can't close on its own:
//
//   - Self-link URLs vs bare names. CAI returns
//     `https://www.googleapis.com/compute/v1/projects/X/global/networks/foo`;
//     TF stores `foo`. selfLinkToBareName(field) collapses any
//     self-link string to its trailing segment.
//
//   - Network tags wrapper. `google_compute_instance` /
//     `google_compute_firewall` surface tags as `{"items": [...]}`;
//     TF flattens to a bare list. flattenNetworkTags() unwraps the
//     `items` envelope so the renamer feeds a clean list to the
//     generated Layer-1 field.
//
// Each helper is intentionally narrow: one transform per helper, no
// type-specific magic. chain composes them in registration order. A
// nil entry in chain is a silent no-op so callers can build the chain
// conditionally without per-type branches.
//
// Implementation: every helper round-trips through encoding/json and a
// map[string]any so helpers can be composed without knowing what the
// next helper expects. The cost is one Marshal + one Unmarshal per
// helper; the enricher runs once per resource per scan, so the
// constant factor is negligible against the CAI SDK call.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Normalizer is the per-type hook on cloudAssetConfig.Normalizer
// (#510). Each call receives the raw CAI JSON and returns the
// transformed bytes ready for the next stage. Returning an error
// short-circuits the chain and fails the fetch — the enricher wraps
// the error with the type context so the dispatcher can attribute the
// failure.
type Normalizer = func(json.RawMessage) (json.RawMessage, error)

// chain composes Normalizers in order. An empty list (or one whose
// entries are all nil) returns a no-op normalizer that passes the
// input through unchanged — convenient for registration sites that
// build the chain conditionally without per-type branches.
//
// Nil entries in the middle of the list are silently skipped so a
// caller can write chain(selfLinkToBareName(...), maybeFlattenTags())
// where maybeFlattenTags returns nil for types whose tag shape already
// matches.
func chain(ns ...Normalizer) Normalizer {
	// Compact to drop nil entries up-front so the hot-path closure
	// avoids the check per resource.
	compact := make([]Normalizer, 0, len(ns))
	for _, n := range ns {
		if n != nil {
			compact = append(compact, n)
		}
	}
	if len(compact) == 0 {
		return func(in json.RawMessage) (json.RawMessage, error) { return in, nil }
	}
	if len(compact) == 1 {
		return compact[0]
	}
	return func(in json.RawMessage) (json.RawMessage, error) {
		cur := in
		for i, n := range compact {
			next, err := n(cur)
			if err != nil {
				return nil, fmt.Errorf("normalizer step %d: %w", i, err)
			}
			cur = next
		}
		return cur, nil
	}
}

// selfLinkToBareName returns a Normalizer that, for the top-level
// string at the given key, collapses a Google self-link URL down to
// its trailing segment — mirroring the provider's flatten of fields
// like `machine_type`, `network`, `disk_type`. The CAI body returns
// the full self-link
// (`https://www.googleapis.com/compute/v1/projects/X/global/networks/foo`);
// Terraform state stores the bare name (`foo`).
//
// Idempotent: a value without a `/` passes through unchanged. If the
// field is absent or not a string, the payload also passes through.
// Operates only on the top-level field — nested-self-link rewrites
// belong in a bespoke normalizer (or in a deeper helper if a real
// type needs it).
func selfLinkToBareName(key string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if key == "" {
			return in, nil
		}
		m, err := decodeCAIObject(in)
		if err != nil {
			return nil, fmt.Errorf("selfLinkToBareName(%q): %w", key, err)
		}
		if m == nil {
			return in, nil
		}
		raw, ok := m[key]
		if !ok || raw == nil {
			return in, nil
		}
		s, ok := raw.(string)
		if !ok {
			return in, nil
		}
		short := shortFromGCPSelfLink(s)
		if short == s {
			return in, nil
		}
		m[key] = short
		return encodeCAIObject(m)
	}
}

// selfLinkSliceToBareNames returns a Normalizer that collapses every
// entry in a top-level string-list to its trailing self-link segment.
// Used for fields like `resource_policies` whose API returns a list of
// self-link URLs while Terraform stores the bare short names.
//
// Idempotent: entries without a `/` pass through unchanged. If the
// field is absent or not a list-of-strings, the payload passes through.
// Non-string entries inside the list are preserved untouched (a
// defensive choice — a CAI payload with a malformed entry should not
// silently disappear).
func selfLinkSliceToBareNames(key string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if key == "" {
			return in, nil
		}
		m, err := decodeCAIObject(in)
		if err != nil {
			return nil, fmt.Errorf("selfLinkSliceToBareNames(%q): %w", key, err)
		}
		if m == nil {
			return in, nil
		}
		raw, ok := m[key]
		if !ok || raw == nil {
			return in, nil
		}
		lst, ok := raw.([]any)
		if !ok {
			return in, nil
		}
		changed := false
		out := make([]any, len(lst))
		for i, entry := range lst {
			s, ok := entry.(string)
			if !ok {
				out[i] = entry
				continue
			}
			short := shortFromGCPSelfLink(s)
			if short != s {
				changed = true
			}
			out[i] = short
		}
		if !changed {
			return in, nil
		}
		m[key] = out
		return encodeCAIObject(m)
	}
}

// flattenNetworkTags returns a Normalizer that unwraps the GCE
// `tags: { items: [...] }` envelope to a bare list at the same key.
// google_compute_instance and google_compute_firewall both surface
// network tags this way in CAI; the generated Layer-1 struct's `tags`
// field expects a flat `[]*Value[string]`, matching the Terraform
// resource attribute. Mirrors AWS's flattenTagList for shape parity
// but keys are bare list entries (network tags carry no key/value
// pairs).
//
// Idempotent: if `tags` is absent, the payload passes through
// unchanged. If `tags` is already a list (not an object), the payload
// also passes through — supports re-runs and hand-built fixtures that
// already flattened. Returns an error for a `tags` value of an
// unexpected shape (e.g. a bare scalar) so the malformed payload
// surfaces at the normalizer rather than cascading into a confusing
// unmarshal error.
//
// Note: the `tags` key here is the GCE network-tags wrapper (string
// list). Resource labels (`labels`) are unrelated — labels are
// already a flat map[string]string in both CAI and TF.
func flattenNetworkTags() Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		m, err := decodeCAIObject(in)
		if err != nil {
			return nil, fmt.Errorf("flattenNetworkTags: %w", err)
		}
		if m == nil {
			return in, nil
		}
		raw, ok := m["tags"]
		if !ok || raw == nil {
			return in, nil
		}
		switch v := raw.(type) {
		case []any:
			// Already flat — leave it alone.
			return in, nil
		case map[string]any:
			items, hasItems := v["items"]
			if !hasItems || items == nil {
				// Empty wrapper — drop it (no useful tags).
				delete(m, "tags")
				return encodeCAIObject(m)
			}
			lst, ok := items.([]any)
			if !ok {
				return nil, fmt.Errorf("flattenNetworkTags: tags.items has unexpected shape %T", items)
			}
			m["tags"] = lst
			return encodeCAIObject(m)
		default:
			return nil, fmt.Errorf("flattenNetworkTags: tags has unexpected shape %T", raw)
		}
	}
}

// shortFromGCPSelfLink trims a Google self-link
// (`https://.../<collection>/<name>` or
// `projects/.../<collection>/<name>`) down to its trailing segment.
// Mirrors the shortFromSelfLink helper in compute_instance_enrich.go
// — kept duplicated rather than dragging the hand-rolled enricher's
// helpers into the generic path's import surface (so #511 retirements
// can delete the hand-rolled file without cascading edits here).
func shortFromGCPSelfLink(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// decodeCAIObject is the shared parse step. Returns (nil, nil) for an
// empty / null payload so helpers can pass-through cleanly without
// special-casing.
func decodeCAIObject(in json.RawMessage) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(in))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(in, &m); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	return m, nil
}

// encodeCAIObject is the shared re-marshal step. Centralized so any
// future MarshalIndent / sort-keys policy lives in one place.
func encodeCAIObject(m map[string]any) (json.RawMessage, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}
	return b, nil
}
