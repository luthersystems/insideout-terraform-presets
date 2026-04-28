package policy

// FieldRole classifies the structural purpose of a field. It is the only
// axis that is required on every FieldPolicy entry — the zero value fails
// Valid() and is rejected by Lint.
type FieldRole string

const (
	// RoleIdentity is what makes a resource itself: arn, id, name, region.
	// Identity fields are visible for context and diffs but never edited
	// by Riley.
	RoleIdentity FieldRole = "Identity"
	// RoleWiring is a cross-reference to another managed resource:
	// kms_key_id, subnet_ids, role_arn, redrive_policy. The composer's
	// graph resolver owns these; Riley edits them through proposed graph
	// changes, never as raw scalars.
	RoleWiring FieldRole = "Wiring"
	// RoleTuning is everything else — knobs Riley can plausibly turn:
	// visibility_timeout_seconds, retention_in_days, lifecycle rules.
	RoleTuning FieldRole = "Tuning"
)

// Valid reports whether r is one of the known role consts.
func (r FieldRole) Valid() bool {
	switch r {
	case RoleIdentity, RoleWiring, RoleTuning:
		return true
	}
	return false
}

// FieldPillar tags a field with the operational concern that justifies
// curating it. It is informational — the lint does not require a non-empty
// pillar — but it lets downstream UIs group fields by Security /
// Performance / Reliability for review screens.
type FieldPillar string

const (
	PillarNone        FieldPillar = ""
	PillarSecurity    FieldPillar = "Security"
	PillarPerformance FieldPillar = "Performance"
	PillarReliability FieldPillar = "Reliability"
)

// Valid reports whether p is one of the known pillar consts. The empty
// string is intentionally valid: most fields do not map cleanly to a
// pillar.
func (p FieldPillar) Valid() bool {
	switch p {
	case PillarNone, PillarSecurity, PillarPerformance, PillarReliability:
		return true
	}
	return false
}

// VisibilityPolicy controls who, if anyone, can see the field. The zero
// value is VisibilityHidden — the safe default for any field that has
// not been deliberately curated.
type VisibilityPolicy string

const (
	// VisibilityHidden — invisible to Riley and the UI. The composer
	// still round-trips the value because Layer 1 preserves it; only
	// system code may inspect it.
	VisibilityHidden VisibilityPolicy = "Hidden"
	// VisibilityRileyVisible — Riley can read the field in chat context
	// and propose changes (subject to EditPolicy).
	VisibilityRileyVisible VisibilityPolicy = "RileyVisible"
	// VisibilityUIVisible — exposed in the product UI / diff screens for
	// a human operator. Implies Riley-visible.
	VisibilityUIVisible VisibilityPolicy = "UIVisible"
)

// Valid reports whether v is one of the known visibility consts. The
// empty string is treated as Hidden; Valid() rejects it so callers must
// state the choice explicitly when constructing a policy.
func (v VisibilityPolicy) Valid() bool {
	switch v {
	case VisibilityHidden, VisibilityRileyVisible, VisibilityUIVisible:
		return true
	}
	return false
}

// EditPolicy controls how the field may be changed. See
// docs/managed-resource-tiers.md "Editor authority by population" for the
// full matrix; in short:
//
//   - EditNever — readable but immutable from any flow.
//   - EditChatSafe — Riley may change it through normal chat.
//   - EditRequiresApproval — Riley proposes; user must confirm against
//     the concrete plan.
//   - EditRelationshipOnly — Riley cannot scalar-edit; the graph
//     resolver / composer manages the value.
//   - EditSystemOnly — only importer / composer system code writes here
//     (tags, labels, provenance).
type EditPolicy string

const (
	EditNever            EditPolicy = "Never"
	EditChatSafe         EditPolicy = "ChatSafe"
	EditRequiresApproval EditPolicy = "RequiresApproval"
	EditRelationshipOnly EditPolicy = "RelationshipOnly"
	EditSystemOnly       EditPolicy = "SystemOnly"
)

func (e EditPolicy) Valid() bool {
	switch e {
	case EditNever, EditChatSafe, EditRequiresApproval, EditRelationshipOnly, EditSystemOnly:
		return true
	}
	return false
}

// SensitivityPolicy controls how the field's value is treated by display
// and diff machinery. The provider-schema "Sensitive=true" flag is the
// upstream input; Layer 2 owns the final classification per
// docs/managed-resource-tiers.md decision #36.
type SensitivityPolicy string

const (
	// SensitivityPublic — safe to show in Riley context and diffs.
	SensitivityPublic SensitivityPolicy = "Public"
	// SensitivityRedacted — show existence and change metadata but not
	// raw values.
	SensitivityRedacted SensitivityPolicy = "Redacted"
	// SensitivitySensitive — hidden from Riley and raw diffs; only
	// system code may retain the value.
	SensitivitySensitive SensitivityPolicy = "Sensitive"
)

// Valid treats the empty string as a synonym for Public so the common
// case of an unset axis on Tuning fields stays valid.
func (s SensitivityPolicy) Valid() bool {
	switch s {
	case "", SensitivityPublic, SensitivityRedacted, SensitivitySensitive:
		return true
	}
	return false
}

// ChangeRiskPolicy expresses what kind of plan a value change implies.
// It overlays the schema-level ReplacementBehavior in Layer 1: where
// Layer 1 says ReplacementUnknown for everything (terraform-json strips
// force_new), Layer 2 records the curator's knowledge.
type ChangeRiskPolicy string

const (
	// ChangeInPlace — the provider updates the resource in place.
	ChangeInPlace ChangeRiskPolicy = "InPlace"
	// ChangeMayReplace — the provider may replace depending on the
	// concrete value; treat as replacement for confirmation purposes.
	ChangeMayReplace ChangeRiskPolicy = "MayReplace"
	// ChangeAlwaysReplace — known destroy/recreate.
	ChangeAlwaysReplace ChangeRiskPolicy = "AlwaysReplace"
	// ChangeUnknown — not curated yet; the apply gate falls back to
	// MayReplace workflow per decision #46.
	ChangeUnknown ChangeRiskPolicy = "Unknown"
)

// Valid treats the empty string as a synonym for ChangeUnknown so the
// common case of leaving the axis unset is not a lint failure.
func (c ChangeRiskPolicy) Valid() bool {
	switch c {
	case "", ChangeInPlace, ChangeMayReplace, ChangeAlwaysReplace, ChangeUnknown:
		return true
	}
	return false
}
