package composer

import (
	"encoding/json"
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
//   - imported_resource_dangling_parent — Identity.ParentAddress is set but
//     no resource in the set carries that Address. The per-instance parent
//     reference (#650) must point at a discovered resource; a dangling
//     reference means the resolver linked to a parent that is not present.
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
	// parentRefs records, per resource index, a non-empty
	// Identity.ParentAddress so the dangling-parent check can run after
	// every Address in the set is known.
	parentRefs := map[int]string{}

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

		if parent := strings.TrimSpace(ir.Identity.ParentAddress); parent != "" {
			parentRefs[i] = parent
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

		// Required-argument completeness is intentionally NOT checked
		// here — see ValidateImportedEmitReadiness. ValidateImportedResources
		// runs on the discovery manifest, an INTERMEDIATE artifact whose
		// resources are still being enriched (dep-chase appends resources,
		// drift-fix converges, the Cloud Control enricher lands typed
		// Attrs in a later pass). Required args are guaranteed only at
		// compose/emit time; gating the manifest writer on them would
		// reject a perfectly valid mid-pipeline snapshot.
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

	// Dangling parent-instance reference (#650): every non-empty
	// ParentAddress must resolve to a resource Address present in the
	// set. Sorted by index for deterministic issue ordering.
	danglingIdxs := make([]int, 0, len(parentRefs))
	for i := range parentRefs {
		danglingIdxs = append(danglingIdxs, i)
	}
	sort.Ints(danglingIdxs)
	for _, i := range danglingIdxs {
		parent := parentRefs[i]
		if _, ok := addressIndex[parent]; ok {
			continue
		}
		issues = append(issues, ValidationIssue{
			Field:  importedField(irs[i], i),
			Value:  parent,
			Code:   "imported_resource_dangling_parent",
			Reason: fmt.Sprintf("Identity.ParentAddress %q does not match any imported resource Address in the set; the per-instance parent reference must point at a discovered resource", parent),
		})
	}

	return issues
}

// CodeImportedDanglingParent is the validation code emitted by
// ValidateImportedResources when a resource's Identity.ParentAddress does not
// resolve to any Address in the set. It is exported so callers can classify it
// as a per-resource drop-and-continue condition rather than a fatal abort
// (#736) — see IsDanglingParentIssue and PartitionDanglingParentIssues.
const CodeImportedDanglingParent = imported.DanglingParentReason

// IsDanglingParentIssue reports whether a ValidationIssue is the
// imported_resource_dangling_parent orphan condition (#736). That condition is
// recoverable: the offending child references a parent no longer in the set, so
// the caller can drop the orphan and continue rather than abort the whole
// operation. Every other code returned by ValidateImportedResources signals a
// genuinely malformed resource (wrong cloud, missing address/import-id, decode
// failure, address collision, …) and stays fatal.
func IsDanglingParentIssue(i ValidationIssue) bool {
	return i.Code == CodeImportedDanglingParent
}

// PartitionDanglingParentIssues splits issues into the genuinely-fatal set and
// the recoverable imported_resource_dangling_parent orphan set (#736). Callers
// that want partial tolerance — drop the orphaned child + warn, abort only on
// fatal issues — use this to scope the non-fatal downgrade narrowly to the
// dangling-parent case. Both slices preserve input order and are nil when
// empty.
func PartitionDanglingParentIssues(issues []ValidationIssue) (fatal []ValidationIssue, dangling []ValidationIssue) {
	for _, i := range issues {
		if IsDanglingParentIssue(i) {
			dangling = append(dangling, i)
			continue
		}
		fatal = append(fatal, i)
	}
	return fatal, dangling
}

// ValidateImportedEmitReadiness runs the emit-time-only checks on irs:
// checks that are meaningful only once discovery has finished and the
// composer is about to render `/imported.tf`. Unlike
// ValidateImportedResources (which validates the INTERMEDIATE discovery
// manifest — a snapshot whose resources are still being enriched by
// dep-chase / drift-fix / Cloud Control passes), this function assumes a
// final, ready-to-emit resource set.
//
// Issue codes:
//
//   - imported_resource_missing_required_attr — an emit-eligible resource
//     is missing one or more schema-Required arguments. EmitImportedTF
//     renders a `resource {}` block per emit-eligible resource and the
//     deploy wrapper runs a plain `terraform plan` (no
//     `-generate-config-out`), so Terraform does NOT backfill required
//     arguments from the imported state — the resource block must already
//     carry every Required attribute. A sparse discovery payload (e.g. an
//     `aws_lambda_function` whose Cloud Control enrichment soft-failed,
//     leaving Attrs empty) would otherwise emit a partial block that
//     fails plan with an opaque "Missing required argument". This surfaces
//     it with the exact missing keys at compose time instead.
//
// The check is skipped for resources flagged by ValidateImportedResources
// for the wrong / unsupported cloud (they are not emitted into this
// compose) and for removed-block-mode resources (which emit no resource
// body). compose.go runs this alongside ValidateImportedResources before
// EmitImportedTF; the discovery manifest writer deliberately does not.
func ValidateImportedEmitReadiness(cloud string, irs []imported.ImportedResource) []ValidationIssue {
	if len(irs) == 0 {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(cloud))
	var issues []ValidationIssue
	for i, ir := range irs {
		if !isImportedTier(ir.Tier) {
			continue
		}
		got := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
		// Wrong / unsupported cloud — not emitted into this compose;
		// ValidateImportedResources already reports the cloud issue.
		if got != "aws" && got != "gcp" {
			continue
		}
		if want != "" && got != want {
			continue
		}
		mode := classifyEmitMode(ir)
		if mode != emitModeResourceImport && mode != emitModeResourceOnly {
			continue
		}
		if missing := missingRequiredAttrs(ir); len(missing) > 0 {
			issues = append(issues, ValidationIssue{
				Field: importedField(ir, i),
				Value: ir.Identity.Type,
				Code:  "imported_resource_missing_required_attr",
				Reason: fmt.Sprintf(
					"imported %s %q is missing required argument(s) %s; discovery did not capture them and Terraform plan will fail — the resource block is not plannable",
					ir.Identity.Type, ir.Identity.Address, strings.Join(missing, ", ")),
				Suggestion: "re-run discovery for this resource, or verify the cloud-credential scope can read the resource's full configuration",
			})
		}
	}
	return issues
}

// dropUncomposable returns irs with every resource flagged
// imported_resource_missing_required_attr removed. emitReadiness is the
// ValidationIssue slice from ValidateImportedEmitReadiness for the same
// resource set.
//
// A resource missing required arguments cannot be rendered as a
// plannable `resource {}` block — `terraform plan` rejects the partial
// body with "Missing required argument" and aborts planning for the
// entire stack (#652). Refusing the single un-composable resource keeps
// the rest of the stack composable; the issue is already recorded so the
// caller still sees which resource was dropped and why.
//
// The input slice is returned unchanged (no allocation) when nothing is
// flagged — the common case.
func dropUncomposable(irs []imported.ImportedResource, emitReadiness []ValidationIssue) []imported.ImportedResource {
	refused := map[string]bool{}
	for _, is := range emitReadiness {
		if is.Code == "imported_resource_missing_required_attr" {
			refused[strings.TrimPrefix(is.Field, "imported.")] = true
		}
	}
	if len(refused) == 0 {
		return irs
	}
	out := make([]imported.ImportedResource, 0, len(irs))
	for _, ir := range irs {
		if refused[ir.Identity.Address] {
			continue
		}
		out = append(out, ir)
	}
	return out
}

// ProvenanceOpts carries the per-compose context needed by
// ValidateProvenanceConflicts. ImportProjectID is the logical claim/owner ID
// used cross-cloud (decision #46 in docs/managed-resource-tiers.md). The
// session ID is not relevant to the conflict check (sessions are advisory)
// and is omitted from this struct deliberately.
type ProvenanceOpts struct {
	ImportProjectID string
}

// ValidateProvenanceConflicts enforces cross-session mutual exclusion on
// imported resources. Issues:
//
//   - imported_resource_provenance_skipped_no_project_id — emitted once when
//     opts.ImportProjectID is empty but irs is non-empty. Indicates the
//     composer is running in pre-#153 backwards-compatible mode and no
//     provenance tags will be written.
//   - imported_resource_provenance_conflict — the resource already advertises
//     a different InsideOutImportProject / insideout-import-project value and
//     no ForceTakeover is supplied. Hard-fails per design decision #45.
//   - imported_resource_force_takeover_invalid — ForceTakeover is set but
//     missing required audit metadata, or its PreviousOwner does not match
//     the value observed on the resource.
//
// Resources that do not support tags/labels (taggable returns false) are
// skipped silently — they fall back to weak-lock semantics, which rely on
// ResourceIdentity uniqueness already enforced by
// imported_resource_address_collision in ValidateImportedResources.
func ValidateProvenanceConflicts(cloud string, irs []imported.ImportedResource, opts ProvenanceOpts) []ValidationIssue {
	if len(irs) == 0 {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(cloud))
	var issues []ValidationIssue

	if strings.TrimSpace(opts.ImportProjectID) == "" {
		// Backwards-compat path: surface a single advisory issue so the
		// caller knows provenance is disabled, then skip per-resource
		// checks.
		hasEligible := false
		for _, ir := range irs {
			got := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
			if got != "aws" && got != "gcp" {
				continue
			}
			if want != "" && got != want {
				continue
			}
			if !isImportedTier(ir.Tier) {
				continue
			}
			hasEligible = true
			break
		}
		if hasEligible {
			issues = append(issues, ValidationIssue{
				Field:      "imported",
				Code:       "imported_resource_provenance_skipped_no_project_id",
				Reason:     "ComposeStackOpts.ImportProjectID is empty; provenance tags will not be emitted and cross-session mutual exclusion is disabled",
				Suggestion: "set opts.ImportProjectID to the InsideOut stack/session import-project identifier (issue #153)",
			})
		}
		return issues
	}

	for i, ir := range irs {
		got := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
		if got != "aws" && got != "gcp" {
			continue
		}
		if want != "" && got != want {
			continue
		}
		if !isImportedTier(ir.Tier) {
			continue
		}
		// Resources without tag/label support are weak-locked; mutual
		// exclusion falls back to address-uniqueness only.
		if _, ok := taggable(ir); !ok {
			continue
		}

		observed, hasOwner := existingProvenanceProject(ir)
		if !hasOwner {
			continue
		}
		if observed == opts.ImportProjectID {
			continue
		}

		field := importedField(ir, i)
		if ir.ForceTakeover == nil {
			issues = append(issues, ValidationIssue{
				Field:      field,
				Value:      observed,
				Code:       "imported_resource_provenance_conflict",
				Reason:     fmt.Sprintf("imported resource %q is already claimed by import project %q; refusing to overwrite without an audited force-takeover", ir.Identity.Address, observed),
				Suggestion: "set ImportedResource.ForceTakeover with actor, reason, previous_owner, and approved_at to override (audited)",
			})
			continue
		}
		ft := ir.ForceTakeover
		if strings.TrimSpace(ft.Actor) == "" || strings.TrimSpace(ft.Reason) == "" || strings.TrimSpace(ft.PreviousOwner) == "" || ft.ApprovedAt.IsZero() {
			issues = append(issues, ValidationIssue{
				Field:      field,
				Value:      observed,
				Code:       "imported_resource_force_takeover_invalid",
				Reason:     "ForceTakeover requires non-empty Actor, Reason, PreviousOwner, and a non-zero ApprovedAt timestamp",
				Suggestion: "populate every audit field on ImportedResource.ForceTakeover before retrying",
			})
			continue
		}
		if ft.PreviousOwner != observed {
			issues = append(issues, ValidationIssue{
				Field:      field,
				Value:      observed,
				Code:       "imported_resource_force_takeover_invalid",
				Reason:     fmt.Sprintf("ForceTakeover.PreviousOwner %q does not match the observed import project %q on the resource", ft.PreviousOwner, observed),
				Suggestion: "ensure ForceTakeover.PreviousOwner matches the InsideOutImportProject value currently on the cloud resource",
			})
		}
	}

	return issues
}

// missingRequiredAttrs returns the sorted snake_case names of the
// schema-Required attributes that are absent from ir's discovered
// attribute bag. Returns nil when the type has no registered schema
// (fail-open: the long tail of unregistered types runs the opaque-attr
// fallback and the composer can't reason about their required set), when
// the type has no Required fields, or when every Required field is
// present.
//
// "Present" means the key appears in the typed Attrs JSON object (the
// post-discovery payload) OR — for the opaque-attr fallback path — in
// ir.Attributes. A key whose value is JSON null still counts as present:
// the composer emits `attr = null`, which Terraform accepts as an
// explicit value; the un-plannable case this guards against is the key
// being absent entirely.
//
// The synthetic `id` attribute is never treated as required even if a
// provider schema marks it so — it is stripped from emission by
// stripResourceIDAttr and belongs only in the `import {}` block.
func missingRequiredAttrs(ir imported.ImportedResource) []string {
	_, schema, ok := generated.Lookup(ir.Identity.Type)
	if !ok {
		return nil
	}
	present := map[string]bool{}
	if len(ir.Attrs) > 0 {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(ir.Attrs, &obj); err == nil {
			for k := range obj {
				present[k] = true
			}
		}
	}
	for k := range ir.Attributes {
		present[k] = true
	}
	var missing []string
	for name, fs := range schema {
		if !fs.Required {
			continue
		}
		if name == computedResourceIDAttr {
			continue
		}
		if !present[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
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
