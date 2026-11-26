package composer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
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
// is "modified" and FromTier / ToTier carry the move. The InsideOut backend's UI
// distinguishes a tier move from a field-only modification by checking
// whether FromTier and ToTier differ.
//
// Field changes use ResourceFieldDiff so the InsideOut backend can render policy metadata
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

	// Error is set when a side's typed Attrs do not decode against the
	// type's model. Changes is then empty: the diff cannot tell what
	// changed, and must not guess from a partial row.
	Error string `json:"error,omitempty"`

	Changes []ResourceFieldDiff `json:"changes,omitempty"`
}

// ResourceFieldDiff describes a per-field change with the policy metadata
// The InsideOut backend needs to group, label, badge, and redact. Policy fields default
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
	// EditRelationshipOnly. The InsideOut backend uses this to render the change as a
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
// Action="added"; same address on both sides with any Tier or attribute
// difference produces Action="modified". Attribute values are what emit
// reads (importedDesiredAttrs): typed Attrs when set, otherwise the legacy
// Attributes map of a row written before Attrs existed. A row moved from one
// to the other with the same values shows no diff: an expression compares
// by its constant value or its text, a JSON string by the document it
// holds, a one-element list by its block, and the typed side's nested
// blocks and computed-only attributes the legacy row lacks are left out.
// Attrs that do not decode set ResourceDiff.Error and leave Changes empty.
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
//     Redacted=true. This holds at nested paths too (a block's attribute,
//     a map entry, a field inside a legacy JSON string): a value whose
//     contents the policy cannot place, but which has a Hidden or redacted
//     path under it, is redacted whole.
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
// Output is sorted by Address for stable consumption. Always returns a
// non-nil slice — empty when there are no diffs — so VersionDiff.Resources
// marshals as the JSON array `[]` rather than `null`. The InsideOut backend's snapshot
// consumers compare diff JSON byte-for-byte, so nil↔[] flips would churn
// every stack the first time it acquires or sheds an imported resource.
func DiffImportedResources(old, new []imported.ImportedResource) []ResourceDiff {
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
	attrs, err := importedDesiredAttrs(ir)
	if err != nil {
		rd.Error = diffDecodeError(ir, err)
		return rd
	}
	rd.Changes = diffAttributeMaps(ir.Identity.Type, nil, attrs)
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
	attrs, err := importedDesiredAttrs(ir)
	if err != nil {
		rd.Error = diffDecodeError(ir, err)
		return rd
	}
	rd.Changes = diffAttributeMaps(ir.Identity.Type, attrs, nil)
	return rd
}

// modifiedResourceDiff returns a diff for two snapshots of the same address.
// ok=false when nothing visible changed (same Tier and no policy-visible
// attribute deltas) — DiffImportedResources skips no-op modifies entirely.
//
// Keys emit never writes are left out on both sides (dropUnemittedKeys):
// they are not in the composed HCL, so a change to them is noise. When
// exactly one side is a legacy-only row (Attributes and no typed Attrs), the
// typed side's nested blocks that the legacy side lacks are left out too
// (dropLegacyUnheldBlocks): the legacy map never stored them.
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
	oldAttrs, oldErr := importedDesiredAttrs(oldIR)
	newAttrs, newErr := importedDesiredAttrs(newIR)
	if oldErr != nil || newErr != nil {
		if !tierChanged && bytes.Equal(oldIR.Attrs, newIR.Attrs) && reflect.DeepEqual(oldIR.Attributes, newIR.Attributes) {
			return ResourceDiff{}, false
		}
		if newErr != nil {
			rd.Error = diffDecodeError(newIR, newErr)
		} else {
			rd.Error = diffDecodeError(oldIR, oldErr)
		}
		return rd, true
	}
	if legacyOnlyRow(oldIR) != legacyOnlyRow(newIR) {
		tfType := newIR.Identity.Type
		if legacyOnlyRow(oldIR) {
			newAttrs = dropLegacyUnheldBlocks(tfType, oldAttrs, newAttrs)
		} else {
			oldAttrs = dropLegacyUnheldBlocks(tfType, newAttrs, oldAttrs)
		}
	}
	oldAttrs = dropUnemittedKeys(newIR.Identity.Type, oldAttrs)
	newAttrs = dropUnemittedKeys(newIR.Identity.Type, newAttrs)
	rd.Changes = diffAttributeMaps(newIR.Identity.Type, oldAttrs, newAttrs)
	if !tierChanged && len(rd.Changes) == 0 {
		return ResourceDiff{}, false
	}
	return rd, true
}

func diffDecodeError(ir imported.ImportedResource, err error) string {
	return fmt.Sprintf("decode typed Attrs for %q failed: %s", ir.Identity.Type, err.Error())
}

// dropLegacyUnheldBlocks returns typed without the nested blocks the
// legacy map does not have: the legacy map kept only top-level attributes.
// A key the legacy row had and the typed row lost still shows as a removal,
// and a configurable attribute only the typed row has as an addition.
func dropLegacyUnheldBlocks(tfType string, legacy, typed map[string]any) map[string]any {
	goType, _, ok := generated.Lookup(tfType)
	if !ok {
		return typed
	}
	blocks := typedBlockFields(goType)
	out := make(map[string]any, len(typed))
	for k, v := range typed {
		if _, inLegacy := legacy[k]; !inLegacy && blocks[k] {
			continue
		}
		out[k] = v
	}
	return out
}

// dropUnemittedKeys returns attrs without the keys emit never writes: id
// (it belongs in the import block), computed-only attributes, and an empty
// collection that is not Required (#951). Typed emit and
// emitOpaqueAttrsBody skip the same keys.
func dropUnemittedKeys(tfType string, attrs map[string]any) map[string]any {
	_, schema, hasSchema := generated.Lookup(tfType)
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if k == computedResourceIDAttr {
			continue
		}
		if hasSchema {
			if fs, ok := schema[k]; ok && !fs.Configurable() {
				continue
			}
			if isEmptyOpaqueCollection(v) && !generated.KeepEmptyCollection(schema, k) {
				continue
			}
		}
		out[k] = v
	}
	return out
}

// typedBlockFields returns the Terraform names of goType's nested block
// fields (tf tag option block or blocks).
func typedBlockFields(goType reflect.Type) map[string]bool {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	out := map[string]bool{}
	if goType.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < goType.NumField(); i++ {
		name, opts, _ := strings.Cut(goType.Field(i).Tag.Get("tf"), ",")
		if opts == "block" || opts == "blocks" {
			out[name] = true
		}
	}
	return out
}

// diffAttributeMaps walks the union of keys across old and new and emits
// ResourceFieldDiff entries for visible (non-Hidden) paths whose values
// differ. Values are compared through comparableAttrValue, so an
// expression shows as its source text (or its value, for a constant) at any
// depth.
//
// Policy applies at every path, not only the top level: under a key that
// has curated nested entries (environment.variables, a block's attribute),
// the diff descends into the value, drops Hidden paths and redacts
// Sensitive / Redacted ones, so no nested secret reaches the output. For a
// top-level path that is a registered JSON-projection parent, the
// projection paths are diffed individually (best-effort; parse failures
// fall back to one parent diff, redacted when any projection is).
//
// Output is sorted by Path. Empty old and empty new short-circuit to nil so
// the caller gets a clean tail.
func diffAttributeMaps(tfType string, old, new map[string]any) []ResourceFieldDiff {
	if len(old) == 0 && len(new) == 0 {
		return nil
	}
	polMap, _ := policy.Lookup(tfType)
	parents := jsonProjectionParents(tfType, polMap)
	nested := nestedPolicyPrefixes(polMap)

	var diffs []ResourceFieldDiff
	for _, key := range unionKeys(old, new) {
		oldVal := comparableAttrValue(old[key])
		newVal := comparableAttrValue(new[key])
		if parents[key] {
			diffs = append(diffs, diffJSONProjection(tfType, key, oldVal, newVal, polMap)...)
			continue
		}
		diffs = appendValueDiffs(diffs, polMap, nested, key, key, oldVal, newVal)
	}
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Path < diffs[j].Path
	})
	return diffs
}

// appendValueDiffs appends the diffs of one value. path is the display path
// (with [i] for an element of a repeated block); logical is the policy path
// (no indices).
func appendValueDiffs(diffs []ResourceFieldDiff, polMap policy.Map, nested map[string]bool, path, logical string, old, new any) []ResourceFieldDiff {
	entry, hasEntry := polMap[logical]
	if hasEntry && entry.Visibility == policy.VisibilityHidden {
		return diffs
	}
	if hasEntry && redactionRequired(entry) {
		if leafValuesEqual(old, new) || blockFormsEqual(old, new) {
			return diffs
		}
		return append(diffs, makeFieldDiff(path, redactedPlaceholder, redactedPlaceholder, entry, hasEntry))
	}
	if !nested[logical] {
		if leafValuesEqual(old, new) || blockFormsEqual(old, new) {
			return diffs
		}
		return append(diffs, makeFieldDiff(path, stringifyAttr(old), stringifyAttr(new), entry, hasEntry))
	}
	// A JSON-string attribute with curated subpaths (the legacy map's form
	// of push_config and friends) is walked as the object or array it
	// holds, and a one-element list of a block as the block itself.
	old, new = jsonDocOrSelf(old), jsonDocOrSelf(new)
	old, new = unwrapSingleton(old, new), unwrapSingleton(new, old)
	if oldMap, newMap, ok := bothMaps(old, new); ok {
		for _, k := range unionKeys(oldMap, newMap) {
			diffs = appendValueDiffs(diffs, polMap, nested, path+"."+k, logical+"."+k, oldMap[k], newMap[k])
		}
		return diffs
	}
	if oldList, newList, ok := bothLists(old, new); ok {
		if len(oldList) <= 1 && len(newList) <= 1 {
			return appendValueDiffs(diffs, polMap, nested, path, logical, listAt(oldList, 0), listAt(newList, 0))
		}
		for i := 0; i < max(len(oldList), len(newList)); i++ {
			diffs = appendValueDiffs(diffs, polMap, nested, fmt.Sprintf("%s[%d]", path, i), logical, listAt(oldList, i), listAt(newList, i))
		}
		return diffs
	}
	// The two sides have different shapes, so they cannot be walked
	// together. Show each with its Hidden paths dropped and its sensitive
	// paths redacted.
	if leafValuesEqual(old, new) {
		return diffs
	}
	oldSafe := sanitizeAttrValue(polMap, logical, old)
	newSafe := sanitizeAttrValue(polMap, logical, new)
	if leafValuesEqual(oldSafe, newSafe) {
		return append(diffs, ResourceFieldDiff{Path: path, From: redactedPlaceholder, To: redactedPlaceholder, Redacted: true})
	}
	return append(diffs, makeFieldDiff(path, stringifyAttr(oldSafe), stringifyAttr(newSafe), entry, hasEntry))
}

// sanitizeAttrValue returns v with every nested path the policy hides
// dropped and every path it redacts replaced by the placeholder. A JSON
// string is sanitized as the document it holds. Any other scalar at a path
// with Hidden or redacted paths under it is replaced by the placeholder:
// its contents are unknown, so it may hold one of them.
func sanitizeAttrValue(polMap policy.Map, logical string, v any) any {
	v = jsonDocOrSelf(v)
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			child := logical + "." + k
			entry, ok := polMap[child]
			switch {
			case ok && entry.Visibility == policy.VisibilityHidden:
				continue
			case ok && redactionRequired(entry):
				out[k] = redactedPlaceholder
			default:
				out[k] = sanitizeAttrValue(polMap, child, e)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = sanitizeAttrValue(polMap, logical, e)
		}
		return out
	case nil:
		return nil
	}
	if projectionRedacted(logical, polMap) {
		return redactedPlaceholder
	}
	return v
}

// leafValuesEqual compares two encoded values by value, or by their display
// text (the legacy map may hold a number as a string).
func leafValuesEqual(a, b any) bool {
	return attrValuesEqual(a, b) || stringifyAttr(a) == stringifyAttr(b)
}

// jsonDocOrSelf returns the object or array a JSON-string value holds, or
// v.
func jsonDocOrSelf(v any) any {
	if str, ok := v.(string); ok {
		if doc, ok := decodeJSONDocument(str); ok {
			switch doc.(type) {
			case map[string]any, []any:
				return doc
			}
		}
	}
	return v
}

// unwrapSingleton returns the element of v when v is a list of at most one
// block (a map) and other is not a list, so a block stored as a one-element
// list compares with the same block stored as a map. An empty list becomes
// nil when other is a map. A list of one scalar stays a list: ["a"] and "a"
// differ.
func unwrapSingleton(v, other any) any {
	l, ok := v.([]any)
	if !ok || len(l) > 1 {
		return v
	}
	if _, otherList := other.([]any); otherList {
		return v
	}
	_, otherMap := other.(map[string]any)
	if len(l) == 0 {
		if otherMap {
			return nil
		}
		return v
	}
	if _, elemMap := l[0].(map[string]any); elemMap || otherMap {
		return l[0]
	}
	return v
}

// blockFormsEqual reports whether old and new are the same block in
// different forms: a map, a one-element list of it, or a JSON string of
// either.
func blockFormsEqual(old, new any) bool {
	old, new = jsonDocOrSelf(old), jsonDocOrSelf(new)
	old, new = unwrapSingleton(old, new), unwrapSingleton(new, old)
	return leafValuesEqual(old, new)
}

// bothMaps reports whether old and new are maps (or absent), at least one
// present.
func bothMaps(old, new any) (map[string]any, map[string]any, bool) {
	om, oOK := old.(map[string]any)
	nm, nOK := new.(map[string]any)
	return om, nm, (oOK || old == nil) && (nOK || new == nil) && (oOK || nOK)
}

// bothLists reports whether old and new are lists (or absent), at least one
// present.
func bothLists(old, new any) ([]any, []any, bool) {
	ol, oOK := old.([]any)
	nl, nOK := new.([]any)
	return ol, nl, (oOK || old == nil) && (nOK || new == nil) && (oOK || nOK)
}

func listAt(l []any, i int) any {
	if i < len(l) {
		return l[i]
	}
	return nil
}

// nestedPolicyPrefixes returns every proper prefix of a dotted policy path:
// the paths under which the diff must descend to apply nested policy.
func nestedPolicyPrefixes(m policy.Map) map[string]bool {
	out := map[string]bool{}
	for path := range m {
		segs := strings.Split(path, ".")
		for i := 1; i < len(segs); i++ {
			out[strings.Join(segs[:i], ".")] = true
		}
	}
	return out
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

// stringifyAttr renders an attribute value as a stable display string. nil
// stays as the empty string so the From/To delta correctly shows additions
// and removals as " → x" / "x → ". A RawExpr renders as its expression text,
// the form the legacy Attributes map stored references in.
func stringifyAttr(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case RawExpr:
		return t.Expr
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
// back a registered JSON projection in m. Used by diffAttributeMaps to route
// those keys through the projection-aware path. A dotted path that is not a
// registered projection is a nested attribute, which diffAttributeMaps
// descends into instead.
func jsonProjectionParents(tfType string, m policy.Map) map[string]bool {
	parents := map[string]bool{}
	for path := range m {
		i := strings.Index(path, ".")
		if i <= 0 {
			continue
		}
		if _, ok := policy.LookupJSONProjection(tfType, path); ok {
			parents[path[:i]] = true
		}
	}
	return parents
}

// projectionRedacted reports whether any policy path under parent (a JSON
// projection or a nested attribute) is Hidden or must be redacted, so
// showing parent's raw value would leak it.
func projectionRedacted(parent string, polMap policy.Map) bool {
	for path, entry := range polMap {
		if strings.HasPrefix(path, parent+".") && (entry.Visibility == policy.VisibilityHidden || redactionRequired(entry)) {
			return true
		}
	}
	return false
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
		if leafValuesEqual(oldVal, newVal) {
			return nil
		}
		entry, hasEntry := polMap[parent]
		if projectionRedacted(parent, polMap) {
			d := makeFieldDiff(parent, redactedPlaceholder, redactedPlaceholder, entry, hasEntry)
			d.Redacted = true
			return []ResourceFieldDiff{d}
		}
		return []ResourceFieldDiff{makeFieldDiff(parent, stringifyAttr(oldVal), stringifyAttr(newVal), entry, hasEntry)}
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
