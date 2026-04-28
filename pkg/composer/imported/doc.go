// Package imported defines the typed carrier for resources discovered by the
// reverse-Terraform importer (or otherwise observed in the cloud) so the
// composer's IR can describe them alongside preset-managed Components.
//
// This package is the Phase 2 foundation of the imported-resource model. It
// owns identity, tier classification, desired-state storage, and edit/conflict
// metadata. It does not generate provider-specific structs (#145/#146), emit
// HCL (#148), validate the union graph (#150), or extend the diff engine
// (#151) — those are downstream Phase 2 sub-tickets.
//
// The full model is documented in docs/managed-resource-tiers.md, especially
// the sections "How this maps to the current pkg/composer IR" (lines 406-544),
// "ImportedMissing operator actions" (lines 642-665), and "Decisions captured"
// (lines 726+).
//
// Wire format: snake_case JSON keys with omitempty, matching the convention in
// pkg/composer/types.go. Tier values are CamelCase strings ("ComposerNative",
// "ImportedFlat", ...) to match the design contract; MissingAction values are
// snake_case ("remove_from_insideout", ...). Times are RFC3339Nano UTC.
package imported
