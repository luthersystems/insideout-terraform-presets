package policy

// FieldRole classifies the structural purpose of a field. It is the only
// axis that is required on every FieldPolicy entry — the zero value fails
// Valid() and is rejected by Lint.
type FieldRole string

const (
	// RoleIdentity is what makes a resource itself: arn, id, name, region.
	// Identity fields are visible for context and diffs but never edited
	// by the interactive agent.
	RoleIdentity FieldRole = "Identity"
	// RoleWiring is a cross-reference to another managed resource:
	// kms_key_id, subnet_ids, role_arn, redrive_policy. The composer's
	// graph resolver owns these; the interactive agent edits them through
	// proposed graph changes, never as raw scalars.
	RoleWiring FieldRole = "Wiring"
	// RoleTuning is everything else — knobs the interactive agent can
	// plausibly turn: visibility_timeout_seconds, retention_in_days,
	// lifecycle rules.
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
	// VisibilityHidden — invisible to the interactive agent and the UI.
	// The composer still round-trips the value because Layer 1 preserves
	// it; only system code may inspect it.
	VisibilityHidden VisibilityPolicy = "Hidden"
	// VisibilityRileyVisible — the interactive agent can read the field
	// in chat context and propose changes (subject to EditPolicy). The
	// "Riley" naming on this const + wire-format string is pinned by
	// downstream TypeScript consumers (luthersystems/reliable, etc.);
	// renaming is a coordinated cross-repo change tracked separately
	// from the in-repo doc-sweep in #489.
	VisibilityRileyVisible VisibilityPolicy = "RileyVisible"
	// VisibilityUIVisible — exposed in the product UI / diff screens for
	// a human operator. Implies agent-visible.
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
//   - EditChatSafe — the interactive agent may change it through normal chat.
//   - EditRequiresApproval — the interactive agent proposes; user must
//     confirm against the concrete plan.
//   - EditRelationshipOnly — the interactive agent cannot scalar-edit;
//     the graph resolver / composer manages the value.
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
	// SensitivityPublic — safe to show in agent context and diffs.
	SensitivityPublic SensitivityPolicy = "Public"
	// SensitivityRedacted — show existence and change metadata but not
	// raw values.
	SensitivityRedacted SensitivityPolicy = "Redacted"
	// SensitivitySensitive — hidden from the interactive agent and raw
	// diffs; only system code may retain the value.
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

// DriftSemantic classifies how the comparator should interpret a
// curated field when computing drift between a sealed snapshot and a
// fresh live read. The skeleton ships the enum and an additive field
// on FieldPolicy; the comparator that consumes the value lives in
// the eventual pkg/drift/imported package (presets#482).
//
// The empty string is intentionally valid and means "no drift
// comparison" — that is, the field is informational only for drift
// purposes. Every existing policy file in pkg/composer/imported/policy
// pre-dates this axis, so leaving it unset must not be a lint
// failure.
//
// Form note: DriftSemanticLabelFilter eventually carries a key-prefix
// parameter (e.g. ignore `goog-*` labels). The skeleton declares the
// const but defers the wire format for the parameter to presets#482
// — likely either a sibling DriftFilter string field on FieldPolicy
// or an encoded suffix on the value, decided when the comparator
// lands and the real use cases are concrete.
type DriftSemantic string

const (
	// DriftSemanticNone — the comparator skips this field. Default
	// for every uncurated field.
	DriftSemanticNone DriftSemantic = ""
	// DriftSemanticExact — exact equality between snapshot and live.
	DriftSemanticExact DriftSemantic = "Exact"
	// DriftSemanticWholeList — list-valued field compared as a whole
	// (order-sensitive). Used for fields like GCS lifecycle_rule
	// where per-element diffs are not meaningful.
	DriftSemanticWholeList DriftSemantic = "WholeList"
	// DriftSemanticLabelFilter — map-valued field compared after
	// filtering out keys matching a prefix (e.g. `goog-*` labels
	// the provider auto-populates). The prefix wire format is
	// deferred to presets#482.
	DriftSemanticLabelFilter DriftSemantic = "LabelFilter"
)

// Valid reports whether d is one of the known drift-semantic consts.
// The empty string is valid and treated as DriftSemanticNone so that
// existing policy files (all of which pre-date this axis) lint
// cleanly without a sweep.
func (d DriftSemantic) Valid() bool {
	switch d {
	case DriftSemanticNone, DriftSemanticExact, DriftSemanticWholeList, DriftSemanticLabelFilter:
		return true
	}
	return false
}
