package policy

import (
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// FieldView is one row of the Layer-2 projection: a curated field along
// with its current value (resolved from the resource's JSON-shaped attrs)
// and its full policy annotations. Returned in stable alphabetical Path
// order by VisibleFieldsFor / EditableFieldsFor / SystemOwnedFieldsFor.
//
// CurrentValue is the projected JSON value at Path. For paths that
// descend through one or more repeated blocks (e.g.
// "lifecycle_rule.condition.age"), CurrentValue holds a []any with one
// scalar per matched leaf — Layer 2 path grammar treats unbracketed
// repeated-block segments as "all elements" (see path.go). For paths
// that don't resolve to any value in attrs (block absent, key missing),
// CurrentValue is nil. For fields whose effective Sensitivity is
// SensitivitySensitive, CurrentValue is forced to nil even if attrs
// carries a value — the projection layer redacts sensitive values at
// the boundary per decision #36.
type FieldView struct {
	Path         string
	CurrentValue any
	Role         FieldRole
	Pillar       FieldPillar
	Visibility   VisibilityPolicy
	Edit         EditPolicy
	ChangeRisk   ChangeRiskPolicy
	Sensitivity  SensitivityPolicy
}

// VisibleFieldsFor returns every Layer-2-curated field for tfType whose
// Visibility is UIVisible or RileyVisible, in stable Path order, with
// current values projected from attrs.
//
// Provider-Sensitive fields (FieldSchema.Sensitive=true in the Layer 1
// generated schema) are excluded by default — they appear in
// SystemOwnedFieldsFor instead — unless the curator explicitly opens
// them with Sensitivity = SensitivityPublic.
//
// Returns nil if tfType is not registered in the policy package. This
// matches the graceful-degrade choice in diffAttributeMaps; callers that
// must distinguish "unregistered" from "registered with zero visible
// fields" should call Lookup first.
func VisibleFieldsFor(tfType string, attrs map[string]any) []FieldView {
	return projectFields(tfType, attrs, filterVisible)
}

// EditableFieldsFor returns the subset of VisibleFieldsFor whose
// EditPolicy permits a write: ChatSafe, RequiresApproval, or
// RelationshipOnly. EditNever and EditSystemOnly entries are excluded.
//
// The returned rows are a subset of VisibleFieldsFor for the same
// (tfType, attrs) — sensitive fields are excluded by the same default
// as VisibleFieldsFor, so a sensitive field that is also editable per
// its EditPolicy will not appear here.
func EditableFieldsFor(tfType string, attrs map[string]any) []FieldView {
	return projectFields(tfType, attrs, filterEditable)
}

// SystemOwnedFieldsFor returns curated fields the consumer must not
// surface to the user or model: EditPolicy in {Never, SystemOnly},
// Visibility=Hidden, or sensitive-by-default per the schema. Sensitive
// rows always have CurrentValue=nil; non-sensitive system-owned rows
// keep their resolved value so the consumer can render existence and
// change metadata.
func SystemOwnedFieldsFor(tfType string, attrs map[string]any) []FieldView {
	return projectFields(tfType, attrs, filterSystemOwned)
}

// filterDecision describes the outcome of running a path through one of
// the three filters. The shared walker uses this so include/exclude is
// uniform across helpers and the cross-helper invariants (Editable ⊆
// Visible; Visible ∪ SystemOwned covers every curated path) hold by
// construction.
type filterDecision int

const (
	skipField filterDecision = iota
	includeField
)

type filterFn func(fv FieldView, schemaSensitive bool) filterDecision

func filterVisible(fv FieldView, schemaSensitive bool) filterDecision {
	if schemaSensitive && fv.Sensitivity != SensitivityPublic {
		return skipField
	}
	switch fv.Visibility {
	case VisibilityUIVisible, VisibilityRileyVisible:
		return includeField
	}
	return skipField
}

func filterEditable(fv FieldView, schemaSensitive bool) filterDecision {
	if filterVisible(fv, schemaSensitive) == skipField {
		return skipField
	}
	switch fv.Edit {
	case EditChatSafe, EditRequiresApproval, EditRelationshipOnly:
		return includeField
	}
	return skipField
}

func filterSystemOwned(fv FieldView, schemaSensitive bool) filterDecision {
	if schemaSensitive && fv.Sensitivity != SensitivityPublic {
		return includeField
	}
	if fv.Visibility == VisibilityHidden {
		return includeField
	}
	switch fv.Edit {
	case EditNever, EditSystemOnly:
		return includeField
	}
	return skipField
}

func projectFields(tfType string, attrs map[string]any, keep filterFn) []FieldView {
	polMap, ok := Lookup(tfType)
	if !ok {
		return nil
	}
	_, schema, _ := generated.Lookup(tfType)

	paths := make([]string, 0, len(polMap))
	for p := range polMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	out := make([]FieldView, 0, len(paths))
	for _, path := range paths {
		fp := polMap[path]
		schemaSensitive := schema[path].Sensitive

		fv := buildFieldView(path, fp, schemaSensitive)
		if keep(fv, schemaSensitive) == skipField {
			continue
		}
		// Resolve value last so the redaction rule applies uniformly
		// regardless of which filter included the row.
		if fv.Sensitivity != SensitivitySensitive {
			fv.CurrentValue = resolveAttrPath(attrs, path)
		}
		out = append(out, fv)
	}
	return out
}

// buildFieldView constructs the projection row from a FieldPolicy,
// applying the Layer-1-schema Sensitive default: if the schema marks
// the field Sensitive and the curator left Sensitivity unset, surface
// Sensitivity=SensitivitySensitive in the row so downstream consumers
// always see a consistent label.
func buildFieldView(path string, fp FieldPolicy, schemaSensitive bool) FieldView {
	sens := fp.Sensitivity
	if sens == "" && schemaSensitive {
		sens = SensitivitySensitive
	}
	return FieldView{
		Path:        path,
		Role:        fp.Role,
		Pillar:      fp.Pillar,
		Visibility:  fp.Visibility,
		Edit:        fp.Edit,
		ChangeRisk:  fp.ChangeRisk,
		Sensitivity: sens,
	}
}

// resolveAttrPath walks the dotted Layer 2 path into a JSON-unmarshaled
// value tree (map[string]any | []any | scalar). When a segment lands on
// a []any, the walker fans out: it resolves the remainder of the path
// against every element and flattens the leaves into a single []any.
// Returns nil if any segment can't be resolved (block absent, key
// missing).
//
// Limitations (out of scope for v1):
//   - No bracket syntax. Layer 2 paths on the currently-enriched GCP
//     types don't use brackets; the few AWS map-keyed paths (tags["X"])
//     are all marked Visibility=Hidden and never surface through the
//     visible/editable helpers.
//   - No JSON-projection traversal. Paths backed by RegisterJSONProjection
//     (aws_sqs_queue.redrive_policy.*) will return nil because the
//     parent attribute is a JSON-encoded string, not a map. Consumers
//     needing those values must decode the parent themselves until a
//     follow-up extends the walker.
func resolveAttrPath(attrs map[string]any, dottedPath string) any {
	if dottedPath == "" {
		return attrs
	}
	return walkValue(attrs, dottedPath)
}

func walkValue(v any, path string) any {
	if path == "" {
		return v
	}
	switch typed := v.(type) {
	case map[string]any:
		head, rest, _ := strings.Cut(path, ".")
		next, ok := typed[head]
		if !ok {
			return nil
		}
		return walkValue(next, rest)
	case []any:
		var out []any
		for _, elem := range typed {
			r := walkValue(elem, path)
			if r == nil {
				continue
			}
			if rs, ok := r.([]any); ok {
				out = append(out, rs...)
			} else {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}
