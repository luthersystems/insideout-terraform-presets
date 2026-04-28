package imported

// Tier classifies how InsideOut treats a resource: composer-managed,
// imported-and-managed, or externally observed. Values are stable identifiers
// — adding or removing tiers does not renumber existing ones. The wire format
// is the CamelCase string literal (e.g. "ComposerNative"); see
// docs/managed-resource-tiers.md "A note on naming" for rationale.
type Tier string

const (
	TierComposerNative      Tier = "ComposerNative"
	TierComposerGraduated   Tier = "ComposerGraduated"
	TierImportedFlat        Tier = "ImportedFlat"
	TierImportedConformant  Tier = "ImportedConformant"
	TierImportedMissing     Tier = "ImportedMissing"
	TierExternalByPolicy    Tier = "ExternalByPolicy"
	TierExternalUnsupported Tier = "ExternalUnsupported"
)

// Valid reports whether t is one of the known tier consts.
func (t Tier) Valid() bool {
	switch t {
	case TierComposerNative,
		TierComposerGraduated,
		TierImportedFlat,
		TierImportedConformant,
		TierImportedMissing,
		TierExternalByPolicy,
		TierExternalUnsupported:
		return true
	}
	return false
}

// Source identifies which actor produced or last touched a record. The same
// type is used in two contexts:
//
//   - ImportedResource.Source: who created the resource record. Valid values
//     are SourceComposer, SourceImporter, SourceInspector.
//   - FieldEdit.Source: who authored a pending model-side edit. Valid values
//     are SourceRiley, SourceAPI, SourceMCP.
//
// One typed string keeps the wire format simple and matches the single Source
// symbol used throughout docs/managed-resource-tiers.md.
type Source string

const (
	SourceComposer  Source = "composer"
	SourceImporter  Source = "importer"
	SourceInspector Source = "inspector"

	SourceRiley Source = "riley"
	SourceAPI   Source = "api"
	SourceMCP   Source = "mcp"
)

// Valid reports whether s is one of the known source consts.
func (s Source) Valid() bool {
	switch s {
	case SourceComposer, SourceImporter, SourceInspector,
		SourceRiley, SourceAPI, SourceMCP:
		return true
	}
	return false
}

// MissingAction is the operator-chosen remediation when a previously imported
// resource is no longer present in cloud (Tier == TierImportedMissing). The
// composer blocks apply until one of these actions is selected. See
// docs/managed-resource-tiers.md "ImportedMissing operator actions"
// (lines 642-665).
type MissingAction string

const (
	// ActionRemoveFromInsideOut detaches the resource from InsideOut
	// management. The composer may emit a `removed { from = ... lifecycle {
	// destroy = false } }` block to release Terraform state without deleting
	// cloud infrastructure.
	ActionRemoveFromInsideOut MissingAction = "remove_from_insideout"

	// ActionRecreateFromLastImport rolls the resource back to TierImportedFlat
	// using the last desired Attributes. The composer emits the resource block
	// so Terraform recreates it.
	ActionRecreateFromLastImport MissingAction = "recreate_from_last_import"

	// ActionReclaimExisting re-runs discovery for the same ResourceIdentity.
	// If the cloud object exists with matching ownership tags, the resource
	// returns to TierImportedFlat without recreation.
	ActionReclaimExisting MissingAction = "reclaim_existing"
)

// Valid reports whether a is one of the known action consts.
func (a MissingAction) Valid() bool {
	switch a {
	case ActionRemoveFromInsideOut,
		ActionRecreateFromLastImport,
		ActionReclaimExisting:
		return true
	}
	return false
}
