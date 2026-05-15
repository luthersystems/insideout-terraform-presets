package policy

// FieldPolicy is the per-attribute Layer 2 decision for an imported
// resource. See docs/managed-resource-tiers.md "Layer 2 — hand-curated
// field policy map". Six axes vary independently; each has its own typed
// string with its own Valid() — the lint runs Valid() on each before
// checking cross-axis rules.
//
// Rationale is a human-readable note used by the lint to gate
// visible-and-Sensitive entries. Most policies leave it empty.
type FieldPolicy struct {
	Role          FieldRole
	Pillar        FieldPillar
	Visibility    VisibilityPolicy
	Edit          EditPolicy
	Sensitivity   SensitivityPolicy
	ChangeRisk    ChangeRiskPolicy
	DriftSemantic DriftSemantic
	Rationale     string
}

// Map is a curated field policy keyed by Terraform attribute path. See
// path.go for the path grammar.
type Map map[string]FieldPolicy

// tagPolicy is the uniform treatment for tag- and label-shaped
// attributes (tags, tags_all, labels, effective_labels,
// terraform_labels, annotations, effective_annotations). The interactive
// agent never authors these; the importer / composer system writes them
// and the diff layer redacts user-set values during display. Used by
// every curated map.
func tagPolicy() FieldPolicy {
	return FieldPolicy{
		Role:        RoleTuning,
		Visibility:  VisibilityHidden,
		Edit:        EditSystemOnly,
		Sensitivity: SensitivityRedacted,
	}
}

// timeoutsPolicy is the uniform treatment for the singleton timeouts
// block present on most resources (create / update / delete / read).
// Treated as system-owned operational metadata, hidden from the interactive agent.
func timeoutsPolicy() FieldPolicy {
	return FieldPolicy{
		Role:       RoleTuning,
		Visibility: VisibilityHidden,
		Edit:       EditSystemOnly,
	}
}
