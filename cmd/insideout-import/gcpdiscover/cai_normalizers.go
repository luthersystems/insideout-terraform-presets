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
//   - System-managed labels. CAI returns goog-managed labels
//     (`goog-managed`, `goog_internal`, …) alongside user labels; the
//     TF provider's view omits them. dropLabelPrefix(field, prefix)
//     filters by key prefix so the emit layer doesn't introduce
//     permadiff (#511 — replaces the open-coded `HasPrefix(k,"goog-")`
//     filter in the seven hand-rolled GCP enrichers).
//
//   - Fully-qualified resource names. Pub/Sub returns
//     `projects/<p>/topics/<n>`; the TF schema's `name` attribute
//     holds the bare short name. shortenLastSegment(field) trims to
//     the trailing path segment.
//
//   - Provider-side defaults the API omits. The TF schema requires
//     fields the underlying API doesn't model (canonical case:
//     `force_destroy = false` on google_storage_bucket — the GCS API
//     has no such field but the TF schema requires it). setDefaultIfAbsent
//     emits the default so the CAI path matches the hand-rolled
//     mapping's unconditional `LiteralOf(false)`.
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

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
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

// dropLabelPrefix returns a Normalizer that walks the labels-map at the
// given top-level field and drops every key whose name starts with the
// given prefix. The canonical use is filtering out goog-managed labels
// (`goog-managed`, `goog_internal`, …) the GCP backend stamps on
// resources without user intent — the Terraform provider's view doesn't
// surface them and emitting them into the HCL surface would cause
// permadiff (issue #511 — the seven hand-rolled GCP enrichers all open-
// code this same `strings.HasPrefix(k, "goog-")` filter as the only
// post-mapping cleanup pass).
//
// Idempotent: a labels-field that is absent, null, or not a map passes
// through unchanged. An empty result map is removed from the parent
// object entirely (so the emit layer can omit the attribute rather than
// emit a `labels = {}` block on a resource whose only labels were
// goog-managed).
//
// Operates only on the named top-level field. Per-block label scrubbing
// (e.g. labels inside a nested `template` block) belongs in a bespoke
// normalizer with a path-aware walker — none of the #511 retire
// candidates need it.
func dropLabelPrefix(labelsField, prefix string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if labelsField == "" || prefix == "" {
			return in, nil
		}
		m, err := decodeCAIObject(in)
		if err != nil {
			return nil, fmt.Errorf("dropLabelPrefix(%q,%q): %w", labelsField, prefix, err)
		}
		if m == nil {
			return in, nil
		}
		raw, ok := m[labelsField]
		if !ok || raw == nil {
			return in, nil
		}
		labels, ok := raw.(map[string]any)
		if !ok {
			// Labels field exists but isn't a map — leave the
			// payload untouched so the downstream renamer sees the
			// same shape it always would (and surfaces a clean
			// unmarshal error rather than the helper masking a real
			// shape regression).
			return in, nil
		}
		changed := false
		for k := range labels {
			if strings.HasPrefix(k, prefix) {
				delete(labels, k)
				changed = true
			}
		}
		if !changed {
			return in, nil
		}
		if len(labels) == 0 {
			// All entries were goog-managed — drop the empty map so
			// the emit layer omits the attribute entirely. Matches
			// the hand-rolled enricher's `if len(labels) > 0` guard
			// (compute_address_enrich.go:217, pubsub_topic_enrich.gen.go:43).
			delete(m, labelsField)
		} else {
			m[labelsField] = labels
		}
		return encodeCAIObject(m)
	}
}

// shortenLastSegment returns a Normalizer that, for the top-level string
// at the given field, replaces the value with the substring after the
// last `/`. Used for GCP resource-name fields whose API representation
// is a fully-qualified path (`projects/<p>/topics/<n>`,
// `projects/<p>/subscriptions/<n>`, `projects/<p>/secrets/<n>`) while
// Terraform stores the bare short name (`<n>`).
//
// Distinct from selfLinkToBareName only by intent: the underlying
// trim-after-last-slash transform is identical, but the documentation
// trail matters — a future contributor asking "why is this here?"
// should see a name-shortener for resource paths, not a self-link
// trimmer for compute URLs.
//
// Idempotent: a value without a `/` passes through unchanged. Absent
// field / non-string value / null value all pass through.
func shortenLastSegment(field string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if field == "" {
			return in, nil
		}
		m, err := decodeCAIObject(in)
		if err != nil {
			return nil, fmt.Errorf("shortenLastSegment(%q): %w", field, err)
		}
		if m == nil {
			return in, nil
		}
		raw, ok := m[field]
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
		m[field] = short
		return encodeCAIObject(m)
	}
}

// setDefaultIfAbsent returns a Normalizer that sets the given top-level
// field to value iff the field is missing from the object. A field
// present with value null is treated as PRESENT and left alone — the
// caller wanted an explicit null and the helper must not overwrite
// that. Used to emit TF-required defaults the CAI body omits (the
// canonical case is `force_destroy = false` on google_storage_bucket
// — the GCS API has no such field, but the TF schema requires it; the
// hand-rolled storage_bucket_enrich.gen.go::mapStorageBucket
// unconditionally writes `out.ForceDestroy = generated.LiteralOf(false)`,
// and the equivalent on the CAI path is to inject the default here so
// the renamer + Layer-1 unmarshal pipeline lands the same value).
//
// Mutates only the top-level object — nested defaults belong in a
// path-aware variant if one is ever needed.
func setDefaultIfAbsent(field string, value any) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if field == "" {
			return in, nil
		}
		m, err := decodeCAIObject(in)
		if err != nil {
			return nil, fmt.Errorf("setDefaultIfAbsent(%q): %w", field, err)
		}
		if m == nil {
			// Synthesize a minimal object so callers depending on
			// the default-injection contract still get the field
			// landed when the CAI payload was empty. Round-trips
			// cleanly through the downstream renamer.
			m = map[string]any{field: value}
			return encodeCAIObject(m)
		}
		if _, present := m[field]; present {
			return in, nil
		}
		m[field] = value
		return encodeCAIObject(m)
	}
}

// universallyElidedTFFields are top-level attribute names dropped on
// every Terraform resource regardless of the schema's Optional /
// Computed flags. `id` is the canonical case: every resource has it,
// the schema declares it Optional+Computed (so FieldSchema.Configurable
// returns true), but it's the provider-managed resource ID and the
// hand-rolled GCP enrichers uniformly skip it (see mapComputeAddress,
// mapPubsubTopic, mapPubsubSubscription, mapStorageBucket — none assign
// to out.ID). Without this allowlist the CAI fallback would emit `id`
// into ir.Attrs while the hand-rolled path would not, breaking byte-
// equal parity needed for retirement.
//
// Keep this list MINIMAL — it's a parity hack, not a general rule.
// New entries belong here only when they're universally
// "Optional+Computed in schema but treated as computed-only in the
// hand-rolled emit layer".
var universallyElidedTFFields = map[string]bool{
	"id": true,
}

// stripComputedOnlyForType returns a Normalizer that removes top-level
// fields the registered FieldSchema marks as purely computed
// (Computed=true && Required=false && Optional=false) from the raw CAI
// JSON before the Layer-1 unmarshal pipeline (#581).
//
// Per decision #5 (computed-only field elision; see
// docs/managed-resource-tiers.md and pkg/composer/imported/generated/schema.go),
// the composed HCL surface MUST NOT emit fields whose only schema role
// is "server-set on read". The hand-rolled GCP enrichers all open-code
// this rule (mapComputeAddress, mapPubsubTopic, mapStorageBucket each
// list the elided fields in their godoc and never assign to them).
// Without this Normalizer, retiring a hand-rolled enricher and falling
// back to the generic CAI path would silently re-introduce
// creation_timestamp / id / label_fingerprint / self_link / users /
// effective_labels / terraform_labels into ir.Attrs — fields the
// emitter would strip later, but the framework-level invariant would
// be lost (and any consumer reading Attrs directly would see the
// difference).
//
// Lookup precedence: the helper consults generated.Lookup(tfType) at
// each call (not at construction) so a Register that lands after this
// Normalizer is constructed still takes effect. A type with no
// registered schema is fail-open: the input passes through untouched
// (the downstream UnmarshalAttrs will already fail loudly if the type
// is truly unregistered, so no information is lost; and the typed
// fallback path is where wiring bugs surface).
//
// CAI returns lowerCamelCase keys; FieldSchema keys are snake_case.
// The helper bridges by camelToSnakeGCP-renaming each top-level key
// for the lookup. Distinguishes the cases that LOOK computed-only but
// aren't:
//
//   - Optional+Computed: user MAY own the value (e.g. `network_tier`
//     on compute_address). Configurable() returns true; kept — EXCEPT
//     for entries in universallyElidedTFFields (currently just `id`,
//     where the hand-rolled enrichers uniformly skip the field
//     regardless of its Optional+Computed schema role).
//   - Required+Computed: rare but the schema says the user must
//     supply it. Configurable() returns true; kept.
//   - Computed-only (the target): Configurable() returns false; dropped.
//
// Operates only on top-level fields. Nested-block computed-only
// filtering (e.g. inside a `template[0]` block) would need a recursive
// walker — none of the #581 retirement candidates need it; the
// hand-rolled enrichers' decision-#5 list is uniformly top-level.
//
// Idempotent: an absent field is a no-op; an empty object is a no-op;
// a non-object payload (rare — only happens if a normalizer earlier
// in the chain produced one) passes through unchanged.
func stripComputedOnlyForType(tfType string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if tfType == "" {
			return in, nil
		}
		_, schema, ok := generated.Lookup(tfType)
		if !ok || len(schema) == 0 {
			// Fail-open: no registered schema → pass through. The
			// downstream UnmarshalAttrs will already fail with
			// "no registered type" if the type is truly missing.
			return in, nil
		}
		m, err := decodeCAIObject(in)
		if err != nil {
			return nil, fmt.Errorf("stripComputedOnlyForType(%q): %w", tfType, err)
		}
		if m == nil {
			return in, nil
		}
		changed := false
		for k := range m {
			snake := camelToSnakeGCP(k)
			if universallyElidedTFFields[snake] {
				delete(m, k)
				changed = true
				continue
			}
			fs, present := schema[snake]
			if !present {
				// Unknown-to-schema field — keep it. The downstream
				// renamer + UnmarshalAttrs will drop it via json
				// ignore-unknown-keys; preserving it here means a
				// future schema regeneration that adds the field
				// doesn't silently start eliding it.
				continue
			}
			if fs.Computed && !fs.Configurable() {
				delete(m, k)
				changed = true
			}
		}
		if !changed {
			return in, nil
		}
		return encodeCAIObject(m)
	}
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
