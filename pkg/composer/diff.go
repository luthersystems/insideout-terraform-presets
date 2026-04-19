package composer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// ComponentDiff describes a change to one component between two Config versions.
type ComponentDiff struct {
	Component string      `json:"component"`
	Action    string      `json:"action"` // "added", "removed", "modified"
	Changes   []FieldDiff `json:"changes,omitempty"`
	Warnings  []string    `json:"warnings,omitempty"`
}

// FieldDiff describes a change to a single field within a component.
type FieldDiff struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// PricingDiff describes a pricing change for a single component.
type PricingDiff struct {
	Component string  `json:"component"`
	Before    float64 `json:"before"`
	After     float64 `json:"after"`
	Delta     float64 `json:"delta"`
}

// MetadataDiff describes a transition of a stack-level metadata field
// (cloud, architecture) between two versions.
type MetadataDiff struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// VersionDiff is the complete diff between two stack versions.
type VersionDiff struct {
	FromVersion int             `json:"from_version"`
	ToVersion   int             `json:"to_version"`
	Components  []ComponentDiff `json:"components"`
	Metadata    []MetadataDiff  `json:"metadata,omitempty"`
	Pricing     []PricingDiff   `json:"pricing,omitempty"`
	Summary     string          `json:"summary"`
}

// JSONTagName extracts the JSON tag name from a struct field, stripping ",omitempty".
func JSONTagName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" || tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

// IsCloudPrefixed returns true if the tag has an aws_ or gcp_ prefix.
func IsCloudPrefixed(tag string) bool {
	return strings.HasPrefix(tag, "aws_") || strings.HasPrefix(tag, "gcp_")
}

// isComponentField returns true if the struct field is a pointer-to-struct
// with a cloud-prefixed JSON tag (aws_* or gcp_*).
func isComponentField(f reflect.StructField) bool {
	tag := JSONTagName(f)
	if tag == "" || !IsCloudPrefixed(tag) {
		return false
	}
	return f.Type.Kind() == reflect.Ptr && f.Type.Elem().Kind() == reflect.Struct
}

// FormatValue returns a human-readable string representation of a reflect.Value.
func FormatValue(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return ""
		}
		return FormatValue(v.Elem())
	case reflect.String:
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%g", v.Float())
	case reflect.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case reflect.Slice:
		if v.IsNil() || v.Len() == 0 {
			return "[]"
		}
		parts := make([]string, v.Len())
		for i := 0; i < v.Len(); i++ {
			parts[i] = FormatValue(v.Index(i))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.Struct:
		b, err := json.Marshal(v.Interface())
		if err != nil {
			return fmt.Sprintf("%v", v.Interface())
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

// diffStructFields compares two struct values field-by-field, returning FieldDiffs
// for any fields whose formatted values differ.
func diffStructFields(oldVal, newVal reflect.Value) []FieldDiff {
	var diffs []FieldDiff
	t := oldVal.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := JSONTagName(f)
		if tag == "" {
			continue
		}
		oldStr := FormatValue(oldVal.Field(i))
		newStr := FormatValue(newVal.Field(i))
		if oldStr != newStr {
			diffs = append(diffs, FieldDiff{Field: tag, From: oldStr, To: newStr})
		}
	}
	return diffs
}

// DiffConfigs compares two Config structs and returns component-level diffs.
// Only cloud-prefixed pointer-to-struct fields (aws_*, gcp_*) are compared;
// top-level scalars (Region, Cloud) and legacy fields are skipped.
func DiffConfigs(oldCfg, newCfg Config) []ComponentDiff {
	var diffs []ComponentDiff
	oldVal := reflect.ValueOf(oldCfg)
	newVal := reflect.ValueOf(newCfg)
	t := oldVal.Type()

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !isComponentField(f) {
			continue
		}
		tag := JSONTagName(f)
		oldField := oldVal.Field(i)
		newField := newVal.Field(i)
		oldNil := oldField.IsNil()
		newNil := newField.IsNil()

		if oldNil && newNil {
			continue
		}
		if oldNil && !newNil {
			diffs = append(diffs, ComponentDiff{Component: tag, Action: "added"})
			continue
		}
		if !oldNil && newNil {
			diffs = append(diffs, ComponentDiff{Component: tag, Action: "removed"})
			continue
		}
		// Both non-nil: compare sub-struct fields.
		fieldDiffs := diffStructFields(oldField.Elem(), newField.Elem())
		if len(fieldDiffs) > 0 {
			diffs = append(diffs, ComponentDiff{
				Component: tag,
				Action:    "modified",
				Changes:   fieldDiffs,
			})
		}
	}
	return diffs
}

// SummarizeChanges generates a concise human-readable summary from component diffs.
func SummarizeChanges(diffs []ComponentDiff) string {
	if len(diffs) == 0 {
		return "No changes."
	}

	var added, removed, modified []string
	for _, d := range diffs {
		switch d.Action {
		case "added":
			added = append(added, d.Component)
		case "removed":
			removed = append(removed, d.Component)
		case "modified":
			detail := d.Component
			if len(d.Changes) > 0 {
				limit := min(2, len(d.Changes))
				var fieldDetails []string
				for _, c := range d.Changes[:limit] {
					fieldDetails = append(fieldDetails, fmt.Sprintf("%s: %s \u2192 %s", c.Field, HumanizeFieldValue(c.Field, c.From), HumanizeFieldValue(c.Field, c.To)))
				}
				detail += " (" + strings.Join(fieldDetails, ", ") + ")"
				if len(d.Changes) > 2 {
					detail += fmt.Sprintf(" +%d more", len(d.Changes)-2)
				}
			}
			modified = append(modified, detail)
		}
	}

	var parts []string
	if len(added) > 0 {
		parts = append(parts, "Added: "+strings.Join(added, ", ")+".")
	}
	if len(removed) > 0 {
		parts = append(parts, "Removed: "+strings.Join(removed, ", ")+".")
	}
	if len(modified) > 0 {
		parts = append(parts, "Modified: "+strings.Join(modified, ", ")+".")
	}
	return strings.Join(parts, " ")
}

// externalToggleFields lists non-cloud-prefixed fields in Components that
// represent toggleable third-party integrations.
var externalToggleFields = map[string]bool{
	"splunk":        true,
	"datadog":       true,
	"githubactions": true,
}

// isToggleComponentField returns true if the struct field represents a
// toggleable component in the Components struct. It accepts cloud-prefixed
// fields (aws_*, gcp_*) and external third-party toggle fields, but skips
// metadata fields (cloud, architecture, cpu_arch) and legacy unprefixed fields.
func isToggleComponentField(f reflect.StructField) bool {
	tag := JSONTagName(f)
	if tag == "" {
		return false
	}
	if IsCloudPrefixed(tag) {
		return true
	}
	return externalToggleFields[tag]
}

// metadataFields lists Components fields that describe the stack itself
// rather than an individual component toggle. Used by DiffComponents to
// *exclude* these fields from toggle diffs. A strict superset of
// stackMetadataDiffFields — the extra entry (cpu_arch) is excluded from
// toggle diffs AND from metadata diffs.
var metadataFields = map[string]bool{
	"cloud":        true,
	"architecture": true,
	"cpu_arch":     true,
}

// stackMetadataDiffFields are the metadata fields that DiffMetadata reports
// transitions for — the inverse view of metadataFields. cpu_arch is
// intentionally excluded here: it is consumed internally by per-component
// arch (aws_ec2 / gcp_compute) and has no UI tile, so highlighting it
// would be spurious.
var stackMetadataDiffFields = []string{"cloud", "architecture"}

// isComponentActive returns true if a reflect.Value represents an "enabled"
// component toggle. The heuristic: *bool → true, string → non-empty,
// *struct → non-nil.
func isComponentActive(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return false
		}
		// *bool
		if v.Elem().Kind() == reflect.Bool {
			return v.Elem().Bool()
		}
		// *struct (e.g. aws_backups)
		return true
	case reflect.String:
		return v.String() != ""
	case reflect.Bool:
		return v.Bool()
	}
	return false
}

// DiffComponents compares two Components structs and returns diffs for
// components that were added or removed. It only looks at toggle fields
// (cloud-prefixed + external); metadata and legacy fields are skipped.
//
// Removal diffs are annotated with dependency warnings when a removed
// component is still required by another active component.
func DiffComponents(oldComp, newComp Components) []ComponentDiff {
	var diffs []ComponentDiff
	var removed, remaining []ComponentKey
	oldVal := reflect.ValueOf(oldComp)
	newVal := reflect.ValueOf(newComp)
	t := oldVal.Type()

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !isToggleComponentField(f) {
			continue
		}
		tag := JSONTagName(f)
		if metadataFields[tag] {
			continue
		}

		oldActive := isComponentActive(oldVal.Field(i))
		newActive := isComponentActive(newVal.Field(i))

		if newActive {
			remaining = append(remaining, ComponentKey(tag))
		}

		if oldActive == newActive {
			continue
		}
		if !oldActive && newActive {
			diffs = append(diffs, ComponentDiff{Component: tag, Action: "added"})
		} else {
			removed = append(removed, ComponentKey(tag))
			diffs = append(diffs, ComponentDiff{Component: tag, Action: "removed"})
		}
	}

	// Annotate removal diffs with dependency warnings.
	if warnings := ValidateRemovals(removed, remaining); len(warnings) > 0 {
		warnByKey := make(map[ComponentKey]RemovalWarning, len(warnings))
		for _, w := range warnings {
			warnByKey[w.Removed] = w
		}
		for i := range diffs {
			if w, ok := warnByKey[ComponentKey(diffs[i].Component)]; ok {
				deps := make([]string, len(w.DependedBy))
				for j, d := range w.DependedBy {
					deps[j] = string(d)
				}
				diffs[i].Warnings = []string{
					fmt.Sprintf("still required by %s", strings.Join(deps, ", ")),
				}
			}
		}
	}

	return diffs
}

// DiffMetadata returns one MetadataDiff per stack-level metadata field whose
// value changed between oldComp and newComp. Only fields in
// stackMetadataDiffFields (cloud, architecture) are reported; cpu_arch is
// always skipped. Transitions covered: empty->non-empty, non-empty->different,
// non-empty->empty. No-op fields are omitted.
func DiffMetadata(oldComp, newComp Components) []MetadataDiff {
	oldVal := reflect.ValueOf(oldComp)
	newVal := reflect.ValueOf(newComp)
	t := oldVal.Type()

	wanted := make(map[string]bool, len(stackMetadataDiffFields))
	for _, name := range stackMetadataDiffFields {
		wanted[name] = true
	}

	var diffs []MetadataDiff
	for i := 0; i < t.NumField(); i++ {
		tag := JSONTagName(t.Field(i))
		if !wanted[tag] {
			continue
		}
		oldStr := FormatValue(oldVal.Field(i))
		newStr := FormatValue(newVal.Field(i))
		if oldStr == newStr {
			continue
		}
		diffs = append(diffs, MetadataDiff{Field: tag, From: oldStr, To: newStr})
	}
	return diffs
}

// MergeComponentDiffs combines component-toggle diffs (from DiffComponents)
// with config-level diffs (from DiffConfigs) into a single slice. Config diffs
// take precedence when both sources report the same component key, since they
// carry richer FieldDiff detail. The result is sorted by component name.
func MergeComponentDiffs(componentDiffs, configDiffs []ComponentDiff) []ComponentDiff {
	seen := make(map[string]bool, len(configDiffs))
	merged := make([]ComponentDiff, 0, len(configDiffs)+len(componentDiffs))

	for _, d := range configDiffs {
		seen[d.Component] = true
		merged = append(merged, d)
	}
	for _, d := range componentDiffs {
		if !seen[d.Component] {
			merged = append(merged, d)
		}
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Component < merged[j].Component
	})
	return merged
}
