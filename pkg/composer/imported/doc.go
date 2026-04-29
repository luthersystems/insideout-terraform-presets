// Package imported defines the typed carrier for resources discovered by the
// reverse-Terraform importer (or otherwise observed in the cloud) so the
// composer's IR can describe them alongside preset-managed Components.
//
// This package is the Phase 2 foundation of the imported-resource model. It
// owns identity, tier classification, desired-state storage, and edit/conflict
// metadata. Provider-specific typed structs and HCL marshaling live in
// pkg/composer/imported/generated (#145/#146); per-resource field policy in
// pkg/composer/imported/policy (#147); composer emission and validation
// (EmitImportedTF / ValidateImportedResources, #148) in pkg/composer.
//
// Composer/runtime boundary: the composer renders desired state from
// Attributes/Attrs into flat HCL plus permanent `import {}` blocks (see
// EmitImportedTF). Confirming that a real `terraform plan` produces only the
// expected import operations and provenance-tag repairs is a runtime check;
// it lives in the reliable repo, not in the composer's pre-plan validators.
// See docs/managed-resource-tiers.md "Composer responsibilities for imported
// resources" and "Plan acceptance rules".
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
