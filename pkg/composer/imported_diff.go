package composer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// Resource diff actions. Mirror the existing component diff vocabulary so
// downstream renderers can share switch arms.
const (
	ResourceActionAdded    = "added"
	ResourceActionRemoved  = "removed"
	ResourceActionModified = "modified"
)

// ResourceDiff describes the change to one imported resource between two
// stack snapshots. It is the imported-resource analogue of ComponentDiff and
// rides alongside ComponentDiff inside VersionDiff.Resources.
//
// Identity: keyed by Terraform Address (matches the import {} block target
// and the cross-tier resolver). Type and Cloud are kept on the diff for
// rendering convenience so consumers don't have to re-derive them.
//
// Tier transitions: when the same address moves between Tiers (e.g.
// ImportedFlat → ImportedConformant, ImportedFlat → ImportedMissing), Action
// is "modified" and FromTier / ToTier carry the move. Reliable's UI
// distinguishes a tier move from a field-only modification by checking
// whether FromTier and ToTier differ.
//
// Field changes use ResourceFieldDiff so Reliable can render policy metadata
// (role, sensitivity, change-risk) without separately querying the policy
// registry.
type ResourceDiff struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Cloud   string `json:"cloud,omitempty"`

	Action string `json:"action"`

	FromTier imported.Tier `json:"from_tier,omitempty"`
	ToTier   imported.Tier `json:"to_tier,omitempty"`

	// Remediation surfaces the operator-chosen action when the resource
	// transitioned into TierImportedMissing. Empty otherwise.
	Remediation imported.MissingAction `json:"remediation,omitempty"`

	Changes []ResourceFieldDiff `json:"changes,omitempty"`
}

// ResourceFieldDiff describes a per-field change with the policy metadata
// Reliable needs to group, label, badge, and redact. Policy fields default
// to the empty string when no curated entry exists for the path or the
// resource type is unregistered; renderers should treat empty as "no
// curator opinion" and fall back to a generic display.
type ResourceFieldDiff struct {
	Path string `json:"path"`
	From string `json:"from"`
	To   string `json:"to"`

	Role        policy.FieldRole         `json:"role,omitempty"`
	Pillar      policy.FieldPillar       `json:"pillar,omitempty"`
	Sensitivity policy.SensitivityPolicy `json:"sensitivity,omitempty"`
	ChangeRisk  policy.ChangeRiskPolicy  `json:"change_risk,omitempty"`
	EditPolicy  policy.EditPolicy        `json:"edit_policy,omitempty"`

	// Redacted is true when From / To were replaced with placeholders to
	// honor the field's SensitivityPolicy. Diff JSON, logs, and humanized
	// text must never contain the original values for redacted fields.
	Redacted bool `json:"redacted,omitempty"`

	// RelationshipOnly is true when the curated EditPolicy is
	// EditRelationshipOnly. Reliable uses this to render the change as a
	// graph relationship, not as an ordinary scalar edit (decisions #30,
	// #31). The underlying value still diffs so the operator can see the
	// referenced address; chat-side scalar edits are blocked separately
	// by ValidateImportedResourceAuthorization.
	RelationshipOnly bool `json:"relationship_only,omitempty"`
}

// redactedPlaceholder is the surface form of any field value that the
// SensitivityPolicy says must not leak. Single shared constant so the wire
// format never depends on call-site convention.
const redactedPlaceholder = "***"

// DiffImportedResources compares two slices of ImportedResource and returns
// one ResourceDiff per address that differs. Resources in old but not new
// produce Action="removed"; resources in new but not old produce
// Action="added"; same address on both sides with any Tier or Attributes
// difference produces Action="modified".
//
// Field-level diffs respect the curated Layer 2 policy for the resource
// type:
//
//   - Hidden fields (Visibility=Hidden) are excluded from the diff output
//     entirely; system-owned attributes (timeouts, tags, internal markers)
//     should not surface in user-visible diffs.
//   - RelationshipOnly fields appear with the RelationshipOnly flag set so
//     renderers can show them as graph references rather than scalar edits.
//   - Sensitive / Redacted fields have From / To replaced with "***" and
//     Redacted=true.
//   - ChangeRisk, Role, Pillar, and EditPolicy are populated from the
//     curated FieldPolicy so renderers can show plan-tied confirmation
//     badges and policy grouping without re-querying the registry.
//
// When the resource type is not registered in the policy package, fields
// fall through to a generic diff with empty policy metadata; the safe
// default redacts nothing because the carrier itself has no curator
// opinion. (Use ValidateImportedResources for structural checks on
// uncurated types; this function is render-only.)
//
// JSON projections (e.g. redrive_policy.deadLetterTargetArn) are best-
// effort: when the parent attribute is a JSON-string and a projection is
// registered, the diff is emitted at the projection path. Parse failures
// fall back to a raw parent diff so the change is never silently dropped.
//
// Output is sorted by Address for stable consumption.
func DiffImportedResources(old, new []imported.ImportedResource) []ResourceDiff {
	if len(old) == 0 && len(new) == 0 {
		return nil
	}
	oldByAddr := indexImportedByAddress(old)
	newByAddr := indexImportedByAddress(new)

	addrs := sortedUnionAddresses(oldByAddr, newByAddr)
	diffs := make([]ResourceDiff, 0, len(addrs))
	for _, addr := range addrs {
		oldIR, hasOld := oldByAddr[addr]
		newIR, hasNew := newByAddr[addr]
		switch {
		case !hasOld && hasNew:
			diffs = append(diffs, addedResourceDiff(newIR))
		case hasOld && !hasNew:
			diffs = append(diffs, removedResourceDiff(oldIR))
		case hasOld && hasNew:
			if d, ok := modifiedResourceDiff(oldIR, newIR); ok {
				diffs = append(diffs, d)
			}
		}
	}
	if len(diffs) == 0 {
		return nil
	}
	return diffs
}

// sortedUnionAddresses returns the sorted union of map keys from two
// address-indexed snapshots. Single allocation; eliminates the post-hoc
// sort.Slice after random map iteration.
func sortedUnionAddresses(a, b map[string]imported.ImportedResource) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// indexImportedByAddress builds an Address->ImportedResource map. Resources
// with empty Address are skipped — DiffImportedResources is render-only and
// has no way to correlate them; the structural validator surfaces those as
// imported_resource_missing_address.
func indexImportedByAddress(irs []imported.ImportedResource) map[string]imported.ImportedResource {
	out := make(map[string]imported.ImportedResource, len(irs))
	for _, ir := range irs {
		addr := strings.TrimSpace(ir.Identity.Address)
		if addr == "" {
			continue
		}
		out[addr] = ir
	}
	return out
}

// addedResourceDiff renders a "this resource appeared on the new side" diff,
// listing every visible attribute as an addition (from "" to the new value).
func addedResourceDiff(ir imported.ImportedResource) ResourceDiff {
	rd := ResourceDiff{
		Address: ir.Identity.Address,
		Type:    ir.Identity.Type,
		Cloud:   ir.Identity.Cloud,
		Action:  ResourceActionAdded,
		ToTier:  ir.Tier,
	}
	if ir.Tier == imported.TierImportedMissing {
		rd.Remediation = ir.Remediation
	}
	rd.Changes = diffAttributeMaps(ir.Identity.Type, nil, ir.Attributes)
	return rd
}

// removedResourceDiff renders a "this resource went away on the new side"
// diff, listing every visible attribute as a removal (from the old value to
// "").
func removedResourceDiff(ir imported.ImportedResource) ResourceDiff {
	rd := ResourceDiff{
		Address:  ir.Identity.Address,
		Type:     ir.Identity.Type,
		Cloud:    ir.Identity.Cloud,
		Action:   ResourceActionRemoved,
		FromTier: ir.Tier,
	}
	rd.Changes = diffAttributeMaps(ir.Identity.Type, ir.Attributes, nil)
	return rd
}

// modifiedResourceDiff returns a diff for two snapshots of the same address.
// ok=false when nothing visible changed (same Tier and no policy-visible
// attribute deltas) — DiffImportedResources skips no-op modifies entirely.
func modifiedResourceDiff(oldIR, newIR imported.ImportedResource) (ResourceDiff, bool) {
	rd := ResourceDiff{
		Address: newIR.Identity.Address,
		Type:    newIR.Identity.Type,
		Cloud:   newIR.Identity.Cloud,
		Action:  ResourceActionModified,
	}
	tierChanged := oldIR.Tier != newIR.Tier
	if tierChanged {
		rd.FromTier = oldIR.Tier
		rd.ToTier = newIR.Tier
		if newIR.Tier == imported.TierImportedMissing {
			rd.Remediation = newIR.Remediation
		}
	}
	rd.Changes = diffAttributeMaps(newIR.Identity.Type, oldIR.Attributes, newIR.Attributes)
	if !tierChanged && len(rd.Changes) == 0 {
		return ResourceDiff{}, false
	}
	return rd, true
}

// diffAttributeMaps walks the union of keys across old and new and emits
// ResourceFieldDiff entries for visible (non-Hidden) paths whose stringified
// values differ. For each top-level path that the curated map declares as a
// JSON-projection parent, the projection paths are diffed individually
// rather than as a single raw parent diff (best-effort; parse failures fall
// back to raw).
//
// Output is sorted by Path. Empty old and empty new short-circuit to nil so
// the caller gets a clean tail.
func diffAttributeMaps(tfType string, old, new map[string]any) []ResourceFieldDiff {
	if len(old) == 0 && len(new) == 0 {
		return nil
	}
	polMap, _ := policy.Lookup(tfType)
	keys := unionKeys(old, new)
	parents := jsonProjectionParents(polMap)

	var diffs []ResourceFieldDiff
	for _, key := range keys {
		if parents[key] {
			diffs = append(diffs, diffJSONProjection(tfType, key, old[key], new[key], polMap)...)
			continue
		}
		entry, hasEntry := polMap[key]
		if hasEntry && entry.Visibility == policy.VisibilityHidden {
			continue
		}
		oldStr := stringifyAttr(old[key])
		newStr := stringifyAttr(new[key])
		if oldStr == newStr {
			continue
		}
		diffs = append(diffs, makeFieldDiff(key, oldStr, newStr, entry, hasEntry))
	}
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Path < diffs[j].Path
	})
	return diffs
}

// makeFieldDiff applies redaction and policy projection to a raw From/To
// pair. hasEntry distinguishes "no curator opinion" from "explicit zero
// values" — without it we couldn't tell apart the empty default and an
// explicit Visibility=Hidden, but the Hidden case is filtered upstream.
func makeFieldDiff(path, from, to string, entry policy.FieldPolicy, hasEntry bool) ResourceFieldDiff {
	d := ResourceFieldDiff{Path: path, From: from, To: to}
	if !hasEntry {
		return d
	}
	d.Role = entry.Role
	d.Pillar = entry.Pillar
	d.Sensitivity = entry.Sensitivity
	d.ChangeRisk = entry.ChangeRisk
	d.EditPolicy = entry.Edit
	d.RelationshipOnly = entry.Edit == policy.EditRelationshipOnly
	if redactionRequired(entry) {
		d.From = redactedPlaceholder
		d.To = redactedPlaceholder
		d.Redacted = true
	}
	return d
}

// redactionRequired reports whether the curated SensitivityPolicy demands
// the value be replaced with a placeholder before display.
func redactionRequired(entry policy.FieldPolicy) bool {
	switch entry.Sensitivity {
	case policy.SensitivitySensitive, policy.SensitivityRedacted:
		return true
	}
	return false
}

// stringifyAttr renders an Attributes value as a stable display string. nil
// stays as the empty string so the From/To delta correctly shows additions
// and removals as " → x" / "x → ".
func stringifyAttr(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case int, int32, int64, float32, float64, bool:
		return fmt.Sprintf("%v", t)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// unionKeys returns the sorted union of map keys, with nil maps treated as
// empty. Used to compute the diff coverage for two attribute snapshots.
func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jsonProjectionParents returns the set of top-level attribute names that
// are declared as JSON-projection parents in m. Used by diffAttributeMaps
// to route those keys through the projection-aware path.
func jsonProjectionParents(m policy.Map) map[string]bool {
	parents := map[string]bool{}
	for path := range m {
		i := strings.Index(path, ".")
		if i <= 0 {
			continue
		}
		parents[path[:i]] = true
	}
	return parents
}

// diffJSONProjection renders one or more ResourceFieldDiff entries for a
// JSON-string parent attribute. Each registered projection gets its own
// entry when both sides parse cleanly. If either side fails to parse —
// including the asymmetric case where one snapshot is well-formed JSON and
// the other is corrupt — the function falls back to a single raw parent
// diff so the change is never silently dropped and stale projection
// entries from the parsed half are not emitted.
func diffJSONProjection(tfType, parent string, oldVal, newVal any, polMap policy.Map) []ResourceFieldDiff {
	oldMap, oldOK := decodeJSONProjection(oldVal)
	newMap, newOK := decodeJSONProjection(newVal)
	if !oldOK || !newOK {
		oldStr := stringifyAttr(oldVal)
		newStr := stringifyAttr(newVal)
		if oldStr == newStr {
			return nil
		}
		entry, hasEntry := polMap[parent]
		return []ResourceFieldDiff{makeFieldDiff(parent, oldStr, newStr, entry, hasEntry)}
	}
	keys := unionKeys(oldMap, newMap)
	var diffs []ResourceFieldDiff
	for _, sub := range keys {
		full := parent + "." + sub
		entry, hasEntry := polMap[full]
		if hasEntry && entry.Visibility == policy.VisibilityHidden {
			continue
		}
		oldStr := stringifyAttr(oldMap[sub])
		newStr := stringifyAttr(newMap[sub])
		if oldStr == newStr {
			continue
		}
		diffs = append(diffs, makeFieldDiff(full, oldStr, newStr, entry, hasEntry))
	}
	return diffs
}

// decodeJSONProjection accepts either a Go map (Phase 1 typed Attributes)
// or a JSON-string (Terraform's wire shape for redrive_policy and friends)
// and returns it as a map[string]any. Returns ok=false on type mismatch or
// parse failure; callers fall back to raw-parent diffing.
func decodeJSONProjection(v any) (map[string]any, bool) {
	if v == nil {
		return nil, true
	}
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, true
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(t), &out); err != nil {
			return nil, false
		}
		return out, true
	}
	// Reflect-based fallback for json.RawMessage and []byte.
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
		raw := rv.Bytes()
		if len(raw) == 0 {
			return nil, true
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, false
		}
		return out, true
	}
	return nil, false
}
