package awsdiscover

// cloudcontrol_normalizers.go — composable per-type Normalizer helpers
// for the Cloud Control unified enricher (#501, partially addresses
// #490 step 3).
//
// A Normalizer transforms the raw Cloud Control GetResource properties
// JSON before the generic camelToSnake / Layer-1 unmarshal pipeline in
// cloudControlEnricher.fetchAndMap. CloudFormation and Terraform
// schemas diverge in a handful of mechanical ways the renamer can't
// close on its own:
//
//   - Primary-name field aliases. CFN names the primary identifier
//     after the resource ("LogGroupName", "BucketName", "QueueName")
//     while Terraform uses the bare "name" / "bucket" alias.
//     renameField("LogGroupName", "Name") rewrites the JSON key.
//
//   - Tag shape. CFN serializes tags as a list of {Key, Value} objects;
//     Terraform's generated Layer-1 struct uses
//     map[string]*Value[string]. flattenTagList("Tags") collapses the
//     list into the flat map shape.
//
//   - Trailing-:* on certain ARNs. CFN's AWS::Logs::LogGroup.Arn
//     includes a `:*` log-stream wildcard suffix that the Terraform
//     `arn` attribute drops. trimARNStar("Arn") strips it.
//
// Each helper is intentionally narrow: one transform per helper, no
// type-specific magic. chain composes them in registration order. A
// nil-entry in chain is a silent no-op so callers can conditionally
// build the chain at registration time without per-type branches.
//
// Implementation: every helper round-trips through encoding/json and a
// map[string]any so the helpers can be composed without knowing what
// the next helper expects. The cost is one Marshal + one Unmarshal per
// helper; the enricher path runs once per resource per scan, so the
// constant factor is negligible against the GetResource SDK call.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// Normalizer is the per-type hook on cloudControlConfig.Normalizer
// (#501). Each call receives the raw Cloud Control JSON and returns
// the transformed bytes ready for the next stage. Returning an error
// short-circuits the chain and fails the fetch — the enricher wraps
// the error with the type context so dispatcher can attribute the
// failure.
type Normalizer = func(json.RawMessage) (json.RawMessage, error)

// chain composes Normalizers in order. An empty list (or one whose
// entries are all nil) returns a no-op normalizer that passes the
// input through unchanged — convenient for registration sites that
// build the chain conditionally without per-type branches.
//
// Nil entries in the middle of the list are silently skipped so a
// caller can write chain(renameField(...), maybeFlattenTags()) where
// maybeFlattenTags returns nil for types whose tag shape already
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

// renameField returns a Normalizer that renames a top-level JSON key
// from `from` to `to`. Idempotent: if `from` is absent, the payload
// passes through unchanged. If `to` is already present, the existing
// value wins (the rename is a no-op rather than an overwrite) — this
// matches the "rename only what's there" intent and avoids clobbering
// a hand-shaped payload.
//
// Operates only on the top-level object. Nested-field renames are
// out of scope for the #501 baseline; if a per-type case needs nested
// renames, write a bespoke normalizer for it rather than extending
// this helper to a path-DSL.
func renameField(from, to string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if from == "" || to == "" || from == to {
			return in, nil
		}
		m, err := decodeObject(in)
		if err != nil {
			return nil, fmt.Errorf("renameField(%q, %q): %w", from, to, err)
		}
		if m == nil {
			return in, nil
		}
		v, ok := m[from]
		if !ok {
			return in, nil
		}
		if _, exists := m[to]; exists {
			// Target already populated — leave both in place rather
			// than guess which one is authoritative. Downstream
			// unmarshal will pick `to` (the snake-case-renamed key
			// matches the json tag) and silently drop `from`.
			return in, nil
		}
		delete(m, from)
		m[to] = v
		return encodeObject(m)
	}
}

// flattenTagList returns a Normalizer that collapses a CloudFormation
// list-of-{Key,Value} tag set at the given top-level key into the
// shape the generated `Tags map[string]*Value[string]` field expects.
// Skips entries with a non-string Key or a missing Value (a defensive
// choice — a CFN payload with a malformed tag entry would otherwise
// propagate noise into the typed payload).
//
// If the key is absent, the payload passes through unchanged. If the
// key is present but the value is already an object (not a list),
// the payload also passes through — this makes the helper idempotent
// in case a prior normalizer already flattened it.
//
// Returns an error if the key holds a value of an unexpected shape
// (e.g. a bare scalar). Surfacing the malformed shape at the
// normalizer surface beats letting it cascade into a confusing
// unmarshal error later.
//
// **Verbatim sub-tree:** the flat map is wrapped under the
// shapeValueForLayer1 verbatim marker (see verbatimMarkerKey in
// cloudcontrol_enricher.go). Tag *keys* are user data (resource
// names like "Project", "Environment"); applying the
// CFN-CamelCase → snake_case rename to them would corrupt them
// (lowercase / underscore-mangle the operator-chosen identifiers).
// The verbatim wrapper opts the sub-tree out of key renaming while
// still benefiting from the scalar-leaf {"literal": …} wrap.
func flattenTagList(key string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if key == "" {
			return in, nil
		}
		m, err := decodeObject(in)
		if err != nil {
			return nil, fmt.Errorf("flattenTagList(%q): %w", key, err)
		}
		if m == nil {
			return in, nil
		}
		raw, ok := m[key]
		if !ok || raw == nil {
			return in, nil
		}
		switch v := raw.(type) {
		case []any:
			flat := make(map[string]any, len(v))
			for _, entry := range v {
				obj, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				k, ok := obj["Key"].(string)
				if !ok || k == "" {
					continue
				}
				// Value may legitimately be "" — preserve it.
				// But absent or explicit-null Value drops the entry:
				// otherwise flat[k] = nil json-marshals to a bare
				// `null` which fails downstream Value[string]
				// UnmarshalJSON (issue #575). Mirrors the empty-Key
				// / non-object skip paths above — a key without a
				// value isn't a useful tag.
				v, hasValue := obj["Value"]
				if !hasValue || v == nil {
					continue
				}
				flat[k] = v
			}
			m[key] = map[string]any{verbatimMarkerKey: flat}
			return encodeObject(m)
		case map[string]any:
			// Already-shaped: either upstream already wrapped under
			// the verbatim marker (re-run case) — pass through
			// unchanged — or a bare flat map slipped in without the
			// wrapper (e.g. an upstream test fixture or a hand-built
			// payload). In the bare case, wrap it now so the
			// downstream shape transform doesn't corrupt the
			// user-data keys.
			if _, wrapped := v[verbatimMarkerKey]; wrapped && len(v) == 1 {
				return in, nil
			}
			m[key] = map[string]any{verbatimMarkerKey: v}
			return encodeObject(m)
		default:
			return nil, fmt.Errorf("flattenTagList(%q): unexpected shape %T", key, raw)
		}
	}
}

// trimARNStar returns a Normalizer that strips a trailing `:*` suffix
// from a top-level string field at the given key. CloudFormation's
// AWS::Logs::LogGroup.Arn includes the `:*` log-stream wildcard
// suffix that the Terraform `arn` attribute drops; other ARN-bearing
// CFN types may surface the same pattern.
//
// Idempotent: if the field is absent, not a string, or has no `:*`
// suffix, the payload passes through unchanged. Operates only on the
// top-level field — nested-ARN normalization is out of scope.
func trimARNStar(key string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if key == "" {
			return in, nil
		}
		m, err := decodeObject(in)
		if err != nil {
			return nil, fmt.Errorf("trimARNStar(%q): %w", key, err)
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
		trimmed := strings.TrimSuffix(s, ":*")
		if trimmed == s {
			return in, nil
		}
		m[key] = trimmed
		return encodeObject(m)
	}
}

// synthIDFromField returns a Normalizer that copies the value at the
// top-level `src` key into a new top-level `Id` key, matching the
// hand-rolled enricher convention of setting `id == name` for resources
// whose Terraform `id` field mirrors the primary name (e.g.
// aws_cloudwatch_log_group's `id == name == LogGroupName`).
//
// The post-camelToSnake projection turns `Id` into `id`, which lands on
// the generated `ID *Value[string]` field via its `json:"id"` tag.
//
// Idempotent: if `src` is absent or empty, the payload passes through
// unchanged. If `Id` is already present, the existing value wins (the
// synth is a no-op rather than an overwrite) — matches the
// renameField convention so the helper composes cleanly when callers
// build conditional chains.
//
// Operates only on top-level string scalars. Non-string `src` values
// pass through without synthesis — the calling chain places this helper
// AFTER any renameField that lands `src` so the value is the
// post-rename primary-name string.
//
// Use with the camelToSnake projection in mind: the helper writes `Id`
// (not `id`) so the downstream shapeCFNForLayer1 wraps the scalar in
// the `{"literal": …}` envelope the generated `Value[T]` field decodes.
// Writing `id` directly would bypass the projection and land a bare
// scalar that Value[T].UnmarshalJSON rejects.
func synthIDFromField(src string) Normalizer {
	return func(in json.RawMessage) (json.RawMessage, error) {
		if src == "" {
			return in, nil
		}
		m, err := decodeObject(in)
		if err != nil {
			return nil, fmt.Errorf("synthIDFromField(%q): %w", src, err)
		}
		if m == nil {
			return in, nil
		}
		if _, exists := m["Id"]; exists {
			return in, nil
		}
		raw, ok := m[src]
		if !ok || raw == nil {
			return in, nil
		}
		s, ok := raw.(string)
		if !ok || s == "" {
			return in, nil
		}
		m["Id"] = s
		return encodeObject(m)
	}
}

// universallyElidedTFFields are top-level attribute names dropped on
// every Terraform resource regardless of the schema's Optional /
// Computed flags. EMPTY today on the AWS side — diverges from the
// GCP-side analog in cmd/insideout-import/gcpdiscover/cai_normalizers.go
// (#581), which elides `id` universally because the GCP hand-rolled
// enrichers uniformly skip assigning to `out.ID`.
//
// The AWS hand-rolled enrichers do the opposite: they SYNTHESIZE
// `out.ID` from the primary-name field (mapS3Bucket sets ID = bucket;
// the retired mapCloudwatchLogGroup set ID = name). The Cloud Control
// generic path matches this by placing synthIDFromField in the
// Normalizer chain. Elide-id universally here would strip both the
// CFN-leaked `Id` AND the synthesized one — breaking parity for
// every type whose chain already wires synthIDFromField.
//
// Kept as a hook (rather than removed) so a future per-type opt-in
// for AWS resources that legitimately leak `id` from CFN-without-
// synth has a single registration point. New entries belong here
// only when they're universally "Optional+Computed in schema but
// treated as computed-only in the hand-rolled emit layer" across
// EVERY AWS resource (not just one or two).
var universallyElidedTFFields = map[string]bool{}

// stripComputedOnlyForType returns a Normalizer that removes top-level
// fields the registered FieldSchema marks as purely computed
// (Computed=true && Required=false && Optional=false) from the raw
// Cloud Control properties JSON before the camelToSnake / Layer-1
// unmarshal pipeline (#582 — mirror of the GCP-side #581 helper in
// cmd/insideout-import/gcpdiscover/cai_normalizers.go).
//
// Per decision #5 (computed-only field elision; see
// docs/managed-resource-tiers.md and pkg/composer/imported/generated/schema.go),
// the composed HCL surface MUST NOT emit fields whose only schema role
// is "server-set on read". The hand-rolled AWS enrichers all open-
// code this rule in their map<Type> functions (mapS3Bucket,
// mapDynamoDBTable, mapSecretsManagerSecret, etc. — none assign to
// computed-only fields like `arn`, `bucket_domain_name`,
// `hosted_zone_id`, `url`, `creation_date`). Without this Normalizer,
// retiring a hand-rolled enricher and falling back to the generic
// Cloud Control path would silently re-introduce those fields into
// ir.Attrs — fields the emitter would strip later, but the
// framework-level invariant would be lost (and any consumer reading
// Attrs directly would see the difference).
//
// Lookup precedence: the helper consults generated.Lookup(tfType) at
// each call (not at construction) so a Register that lands after this
// Normalizer is constructed still takes effect. A type with no
// registered schema is fail-open: the input passes through untouched
// (the downstream UnmarshalAttrs will already fail loudly if the type
// is truly unregistered, so no information is lost; and the typed
// fallback path is where wiring bugs surface).
//
// CloudFormation returns PascalCase keys (`Arn`, `BucketName`,
// `CreationTime`); FieldSchema keys are snake_case
// (`arn`, `bucket_name`, `creation_time`). The helper bridges by
// camelToSnake-renaming each top-level key for the lookup. Place this
// Normalizer LAST in the chain so it sees the post-rename / post-
// synth payload (renameField may have changed `LogGroupName` to
// `Name`; synthIDFromField may have added `Id` — the post-rename
// snake_case lookup matches the schema). Distinguishes the cases
// that LOOK computed-only but aren't:
//
//   - Optional+Computed: user MAY own the value (e.g. `name` on
//     log_group, `id` on every AWS resource). Configurable() returns
//     true; kept — EXCEPT for entries in universallyElidedTFFields
//     (empty today on the AWS side; see that variable's godoc for
//     why the GCP-side `id` entry doesn't carry over).
//   - Required+Computed: rare but the schema says the user must
//     supply it. Configurable() returns true; kept.
//   - Computed-only (the target): Configurable() returns false;
//     dropped. Canonical AWS examples: `arn`, `url` (sqs_queue),
//     `bucket_domain_name`, `bucket_regional_domain_name`,
//     `hosted_zone_id`, `region`, `website_domain`, `website_endpoint`
//     (s3_bucket), `arn` (log_group).
//
// Operates only on top-level fields. Nested-block computed-only
// filtering (e.g. inside a `lifecycle_rule[0]` block) would need a
// recursive walker — none of the AWS retirement candidates need it
// today; the hand-rolled enrichers' decision-#5 list is uniformly
// top-level.
//
// Idempotent: an absent field is a no-op; an empty object is a no-op;
// a non-object payload (rare — only happens if a normalizer earlier
// in the chain produced one) returns a wrapped error so the chain
// reports the failing helper instead of cascading into a confusing
// unmarshal error downstream.
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
		m, err := decodeObject(in)
		if err != nil {
			return nil, fmt.Errorf("stripComputedOnlyForType(%q): %w", tfType, err)
		}
		if m == nil {
			return in, nil
		}
		changed := false
		for k := range m {
			snake := camelToSnake(k)
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
		return encodeObject(m)
	}
}

// decodeObject is the shared parse step. Returns (nil, nil) for an
// empty / null payload so helpers can pass-through cleanly without
// special-casing.
func decodeObject(in json.RawMessage) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	// Cheap pre-check: a "null" payload decodes to a nil map. Surface
	// that as (nil, nil) so helpers preserve the input untouched.
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

// encodeObject is the shared re-marshal step. Centralized so any
// future MarshalIndent / sort-keys policy lives in one place.
func encodeObject(m map[string]any) (json.RawMessage, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}
	return b, nil
}
