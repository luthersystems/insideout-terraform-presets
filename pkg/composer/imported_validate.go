package composer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// ValidateImportedResources runs structural pre-emission checks on irs in the
// context of a compose for cloud. Issues use snake_case codes matching the
// rest of the composer:
//
//   - imported_resource_unknown_tier — Identity.Tier not in the known set.
//   - imported_resource_unsupported_cloud — Identity.Cloud empty, not in
//     {aws, gcp}, or mismatched against the compose cloud.
//   - imported_resource_missing_address — Identity.Address empty.
//   - imported_resource_missing_import_id — Identity.ImportID empty for an
//     emit-eligible tier (ImportedFlat, ImportedConformant, or
//     ImportedMissing). Phase 1 import requires a non-empty id.
//   - imported_resource_address_collision — two resources resolve to the
//     same Identity.Address.
//   - imported_resource_missing_remediation — TierImportedMissing without
//     Remediation set; the composer blocks emission until the operator picks
//     an action.
//   - imported_resource_decode_failed — Attrs is non-empty but
//     generated.UnmarshalAttrs(Identity.Type, Attrs) returns an error or the
//     type is not in the registry.
//
// External tiers (ExternalByPolicy, ExternalUnsupported) and ComposerNative /
// ComposerGraduated are out of scope here — they are not emitted as flat
// imported HCL.
func ValidateImportedResources(cloud string, irs []imported.ImportedResource) []ValidationIssue {
	if len(irs) == 0 {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(cloud))
	var issues []ValidationIssue
	addressIndex := map[string][]int{}

	for i, ir := range irs {
		field := importedField(ir, i)

		if !ir.Tier.Valid() {
			issues = append(issues, ValidationIssue{
				Field:  field,
				Value:  string(ir.Tier),
				Code:   "imported_resource_unknown_tier",
				Reason: fmt.Sprintf("imported resource has unknown tier %q; expected one of %s", string(ir.Tier), strings.Join(knownTierNames(), ", ")),
			})
			continue
		}

		// Tiers the composer does not emit as imported HCL skip the
		// per-record structural checks. Out-of-scope for #148.
		if !isImportedTier(ir.Tier) {
			continue
		}

		got := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
		switch {
		case got == "":
			issues = append(issues, ValidationIssue{
				Field:  field,
				Code:   "imported_resource_unsupported_cloud",
				Reason: "imported resource has empty Identity.Cloud; expected \"aws\" or \"gcp\"",
			})
		case got != "aws" && got != "gcp":
			issues = append(issues, ValidationIssue{
				Field:   field,
				Value:   ir.Identity.Cloud,
				Allowed: []string{"aws", "gcp"},
				Code:    "imported_resource_unsupported_cloud",
				Reason:  fmt.Sprintf("imported resource has unsupported Identity.Cloud %q; expected \"aws\" or \"gcp\"", ir.Identity.Cloud),
			})
		case want != "" && got != want:
			issues = append(issues, ValidationIssue{
				Field:  field,
				Value:  ir.Identity.Cloud,
				Code:   "imported_resource_unsupported_cloud",
				Reason: fmt.Sprintf("imported resource cloud %q does not match the compose cloud %q; one stack composes a single cloud", ir.Identity.Cloud, want),
			})
		}

		addr := strings.TrimSpace(ir.Identity.Address)
		if addr == "" {
			issues = append(issues, ValidationIssue{
				Field:  field,
				Code:   "imported_resource_missing_address",
				Reason: "imported resource has empty Identity.Address; the importer must populate it via imported.GenerateAddress before composing",
			})
		} else {
			addressIndex[addr] = append(addressIndex[addr], i)
		}

		if strings.TrimSpace(ir.Identity.ImportID) == "" {
			issues = append(issues, ValidationIssue{
				Field:  field,
				Code:   "imported_resource_missing_import_id",
				Reason: "imported resource has empty Identity.ImportID; cannot emit a Terraform import {} block without a provider import id",
			})
		}

		if ir.Tier == imported.TierImportedMissing && !ir.Remediation.Valid() {
			issues = append(issues, ValidationIssue{
				Field:      field,
				Value:      string(ir.Remediation),
				Allowed:    []string{string(imported.ActionRemoveFromInsideOut), string(imported.ActionRecreateFromLastImport), string(imported.ActionReclaimExisting)},
				Code:       "imported_resource_missing_remediation",
				Reason:     "TierImportedMissing requires Remediation; the composer blocks emission until an operator picks an action",
				Suggestion: "set ImportedResource.Remediation to remove_from_insideout, recreate_from_last_import, or reclaim_existing",
			})
		}

		if len(ir.Attrs) > 0 {
			if _, err := generated.UnmarshalAttrs(ir.Identity.Type, ir.Attrs); err != nil {
				issues = append(issues, ValidationIssue{
					Field:  field,
					Value:  ir.Identity.Type,
					Code:   "imported_resource_decode_failed",
					Reason: fmt.Sprintf("decode typed Attrs for %q failed: %s", ir.Identity.Type, err.Error()),
				})
			}
		}
	}

	for addr, idxs := range addressIndex {
		if len(idxs) < 2 {
			continue
		}
		issues = append(issues, ValidationIssue{
			Field:  "imported." + addr,
			Value:  fmt.Sprintf("%d resources", len(idxs)),
			Code:   "imported_resource_address_collision",
			Reason: fmt.Sprintf("Terraform address %q is claimed by %d imported resources; addresses must be unique within a stack", addr, len(idxs)),
		})
	}

	return issues
}

// isImportedTier reports whether t is a tier that the composer emits as flat
// imported HCL (or, for ImportedMissing, would emit once Remediation is set).
func isImportedTier(t imported.Tier) bool {
	switch t {
	case imported.TierImportedFlat,
		imported.TierImportedConformant,
		imported.TierImportedMissing:
		return true
	}
	return false
}

// importedField builds the Field value for a ValidationIssue describing ir.
// Falls back to a stable index-based identifier when Address is empty so
// dedupeAndSortIssues still produces deterministic output.
func importedField(ir imported.ImportedResource, idx int) string {
	if a := strings.TrimSpace(ir.Identity.Address); a != "" {
		return "imported." + a
	}
	return fmt.Sprintf("imported.[%d]", idx)
}

// knownTierNames returns the string forms of every defined Tier in stable
// order for use in error messages.
func knownTierNames() []string {
	names := []string{
		string(imported.TierComposerNative),
		string(imported.TierComposerGraduated),
		string(imported.TierImportedFlat),
		string(imported.TierImportedConformant),
		string(imported.TierImportedMissing),
		string(imported.TierExternalByPolicy),
		string(imported.TierExternalUnsupported),
	}
	sort.Strings(names)
	return names
}
