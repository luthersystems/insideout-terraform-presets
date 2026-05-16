package policy

// FieldPolicy is the per-attribute Layer 2 decision for an imported
// resource. See docs/managed-resource-tiers.md "Layer 2 — hand-curated
// field policy map". Six axes vary independently; each has its own typed
// string with its own Valid() — the lint runs Valid() on each before
// checking cross-axis rules.
//
// Rationale is a human-readable note used by the lint to gate
// visible-and-Sensitive entries. Most policies leave it empty.
//
// LabelDriftIgnorePrefixes is consulted only when DriftSemantic ==
// DriftSemanticLabelFilter. Each entry is a string-prefix match against
// the per-key name in a map[string]any-shaped attribute; matching keys
// are dropped on both sides before the per-key delta is computed. Empty
// (the zero value) falls back to a built-in GCP-noise default of
// {"goog-", "goog_"} so existing policies that pre-date this knob keep
// the historical behavior.
type FieldPolicy struct {
	Role                     FieldRole
	Pillar                   FieldPillar
	Visibility               VisibilityPolicy
	Edit                     EditPolicy
	Sensitivity              SensitivityPolicy
	ChangeRisk               ChangeRiskPolicy
	DriftSemantic            DriftSemantic
	LabelDriftIgnorePrefixes []string
	Rationale                string
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

// gcpLabelDriftIgnorePrefixes is the canonical set of label-key
// prefixes that must be filtered out of GCP label drift on both
// snapshot and live sides before per-key comparison:
//
//   - "goog-" / "goog_" — GCP control-plane auto-populated labels
//     (e.g. goog-managed-by, goog_terraform_provisioned). A Google
//     SRE bumping any of these would otherwise emit drift on every
//     bucket / topic / secret in the inventory.
//   - "insideout-import" — the importer / composer's own provenance
//     labels (insideout-imported, insideout-imported-at,
//     insideout-import-project, insideout-import-session). The four
//     reserved keys all share the prefix without a trailing hyphen,
//     so one short prefix covers them all. Bumping these on
//     re-emission is expected behavior, not drift.
//
// Used by gcpLabelDriftPolicy() and any per-type policy that opts into
// curated label drift. Mirrors reliable's importedDriftLabelPrefixIgnore
// (internal/agentapi/imported_drift.go) so the per-key drift surface is
// identical between the upstream comparator and the legacy comparator
// it replaces (#1479 Surface B).
var gcpLabelDriftIgnorePrefixes = []string{
	"goog-",
	"goog_",
	"insideout-import",
}

// gcpLabelDriftPolicy is the uniform treatment for GCP `labels`-shaped
// map attributes that we want curated drift on. Same axes as
// tagPolicy() — system-owned, redacted from display — but
// DriftSemantic=LabelFilter with the canonical GCP ignore-prefix set
// so the comparator emits one per-key `labels.<key>` mismatch for each
// user-curated label that diverges between snapshot and live.
//
// Use this in lieu of tagPolicy() on any GCP type whose user-set
// labels are part of the drift signal. Adopting gcpLabelDriftPolicy()
// is policy-author opt-in rather than a global flip so types with no
// useful label-drift surface (or types whose label drift is noise) can
// stay on tagPolicy().
func gcpLabelDriftPolicy() FieldPolicy {
	return FieldPolicy{
		Role:                     RoleTuning,
		Visibility:               VisibilityHidden,
		Edit:                     EditSystemOnly,
		Sensitivity:              SensitivityRedacted,
		DriftSemantic:            DriftSemanticLabelFilter,
		LabelDriftIgnorePrefixes: gcpLabelDriftIgnorePrefixes,
	}
}
