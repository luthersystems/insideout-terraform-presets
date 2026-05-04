package composer

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// ValidateImportedResourceAuthorization enforces the Layer 2 field policy on
// model-side write paths to imported resources (chat, API, MCP). It is the
// Phase 2 server-side write authorization sibling of ValidateImportedResources
// (which carries Phase 1 structural checks). See
// docs/managed-resource-tiers.md "Editor authority" section and decisions
// #7, #8, #14, #19, #30, #31, #33, #36, #37, #42, and #43.
//
// For each FieldEdit on an imported resource it produces at most one issue
// per (resource, path) with a snake_case Code drawn from:
//
//   - imported_resource_field_edit_no_policy — the resource type has no
//     Layer 2 policy registered; per decision #43, every imported field is
//     hidden / system-owned by default and edits to unregistered types are
//     rejected.
//   - imported_resource_field_edit_unknown_path — the path does not resolve
//     against the Layer 1 generated struct or any registered JSON projection
//     for the resource type (decision #33).
//   - imported_resource_field_edit_no_policy_for_path — the path resolves
//     but is absent from the curated map; default deny per decision #43.
//   - imported_resource_field_edit_forbidden — Edit=Never. Identity fields
//     (arn, name, region) live here.
//   - imported_resource_field_edit_system_only — Edit=SystemOnly. Tags,
//     labels, and provenance attributes are written by the importer/composer
//     only.
//   - imported_resource_field_edit_relationship_only — Edit=RelationshipOnly.
//     Wiring fields (kms_key_id, redrive_policy.deadLetterTargetArn) cannot
//     be scalar-edited in Phase 2 (decisions #30, #31).
//   - imported_resource_field_edit_requires_approval — Edit=RequiresApproval
//     without an Approval audit record on the FieldEdit.
//   - imported_resource_field_edit_approval_invalid — Approval is set but
//     missing one of Approver, ApprovedAt, or PlanHash.
//   - imported_resource_field_edit_replacement_risk_unconfirmed —
//     ChangeRisk in {MayReplace, AlwaysReplace, Unknown} without an Approval
//     record attesting plan review (decision #42; Unknown follows the
//     MayReplace workflow).
//   - imported_resource_field_edit_reimport_conflict — the resource's
//     current desired Attributes value at this path no longer matches the
//     FieldEdit's NewValue. A re-import or other writer overwrote the
//     pending edit (decision #19); the operator must accept cloud, keep the
//     edit, or abort.
//
// Sensitive field values (Sensitivity=Sensitive or Sensitivity=Redacted) are
// redacted to "***" in any ValidationIssue.Value emitted by this function so
// raw sensitive data cannot escape into validation errors, diffs, or chat
// correction prompts (decision #36).
//
// Resources whose tier is not an imported tier (ComposerNative, External*) or
// whose Identity.Cloud does not match the compose cloud are skipped silently;
// those mismatches are surfaced by ValidateImportedResources, not here.
//
// The function performs no I/O, has no side effects, and returns a stable
// per-input slice. Output is fed through dedupeAndSortIssues by ValidateAll
// like every other validator.
func ValidateImportedResourceAuthorization(cloud string, irs []imported.ImportedResource) []ValidationIssue {
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
		if got != "aws" && got != "gcp" {
			continue
		}
		if want != "" && got != want {
			continue
		}
		if len(ir.FieldEdits) == 0 {
			continue
		}

		polMap, hasPol := policy.Lookup(ir.Identity.Type)
		field := importedField(ir, i)

		for _, path := range sortedFieldEditPaths(ir.FieldEdits) {
			edit := ir.FieldEdits[path]
			issueField := field + "." + path

			if !hasPol {
				issues = append(issues, ValidationIssue{
					Field:  issueField,
					Code:   "imported_resource_field_edit_no_policy",
					Reason: fmt.Sprintf("imported resource type %q has no Layer 2 field policy registered; edits default to hidden / system-owned per decision #43", ir.Identity.Type),
				})
				continue
			}

			// Curated paths short-circuit the reflective ResolvePath walk:
			// presence in the policy map implies resolvability (the lint
			// rejects unresolvable curated paths). Only run ResolvePath to
			// disambiguate "unknown path" vs "uncurated path" for paths
			// the curator hasn't listed.
			entry, hasEntry := polMap[path]
			if !hasEntry {
				if err := policy.ResolvePath(ir.Identity.Type, path); err != nil {
					issues = append(issues, ValidationIssue{
						Field:  issueField,
						Code:   "imported_resource_field_edit_unknown_path",
						Reason: fmt.Sprintf("imported resource path %q does not resolve against %s: %s", path, ir.Identity.Type, err.Error()),
					})
					continue
				}
				issues = append(issues, ValidationIssue{
					Field:  issueField,
					Code:   "imported_resource_field_edit_no_policy_for_path",
					Reason: fmt.Sprintf("imported resource path %q has no curated FieldPolicy on %s; defaults to hidden / system-owned per decision #43", path, ir.Identity.Type),
				})
				continue
			}

			if iss, ok := evaluateEditPolicy(issueField, ir.Identity.Type, path, edit, entry); ok {
				issues = append(issues, iss)
				continue
			}

			if iss, ok := evaluateApproval(issueField, path, edit, entry); ok {
				issues = append(issues, iss)
				continue
			}

			if iss, ok := evaluateChangeRisk(issueField, path, edit, entry); ok {
				issues = append(issues, iss)
				continue
			}

			if iss, ok := evaluateReimportConflict(issueField, path, edit, ir); ok {
				issues = append(issues, iss)
				continue
			}
		}
	}

	return issues
}

// evaluateEditPolicy returns a non-empty issue when the FieldEdit's target
// policy forbids the write outright (Never, SystemOnly, RelationshipOnly,
// RequiresApproval-without-approval, or an unrecognized EditPolicy value).
func evaluateEditPolicy(issueField, tfType, path string, edit imported.FieldEdit, entry policy.FieldPolicy) (ValidationIssue, bool) {
	redacted := redactValue(entry, edit.NewValue)
	switch entry.Edit {
	case policy.EditNever:
		reason := fmt.Sprintf("imported resource path %q is Edit=Never on %s; field is read-only from every model write path", path, tfType)
		if entry.Role == policy.RoleIdentity {
			reason = fmt.Sprintf("imported resource path %q is an identity field (Edit=Never) on %s; identity is immutable from chat/API/MCP", path, tfType)
		}
		return ValidationIssue{
			Field:  issueField,
			Value:  redacted,
			Code:   "imported_resource_field_edit_forbidden",
			Reason: reason,
		}, true
	case policy.EditSystemOnly:
		return ValidationIssue{
			Field:  issueField,
			Value:  redacted,
			Code:   "imported_resource_field_edit_system_only",
			Reason: fmt.Sprintf("imported resource path %q is Edit=SystemOnly on %s; only the importer/composer writes here (tags, labels, provenance)", path, tfType),
		}, true
	case policy.EditRelationshipOnly:
		return ValidationIssue{
			Field:      issueField,
			Value:      redacted,
			Code:       "imported_resource_field_edit_relationship_only",
			Reason:     fmt.Sprintf("imported resource path %q is Edit=RelationshipOnly on %s; raw wiring values are owned by the graph, not scalar-editable in Phase 2 (decision #30)", path, tfType),
			Suggestion: "use a future graph-editing operation to change the relationship",
		}, true
	case policy.EditChatSafe, policy.EditRequiresApproval:
		return ValidationIssue{}, false
	default:
		return ValidationIssue{
			Field:  issueField,
			Value:  redacted,
			Code:   "imported_resource_field_edit_forbidden",
			Reason: fmt.Sprintf("imported resource path %q on %s has unknown EditPolicy %q", path, tfType, string(entry.Edit)),
		}, true
	}
}

// evaluateApproval returns an issue when Edit=RequiresApproval but the
// FieldEdit lacks a complete approval audit record. Edits with non-approval
// EditPolicy values fall through (their gates run elsewhere).
func evaluateApproval(issueField, path string, edit imported.FieldEdit, entry policy.FieldPolicy) (ValidationIssue, bool) {
	if entry.Edit != policy.EditRequiresApproval {
		return ValidationIssue{}, false
	}
	if edit.Approval == nil {
		return ValidationIssue{
			Field:      issueField,
			Value:      redactValue(entry, edit.NewValue),
			Code:       "imported_resource_field_edit_requires_approval",
			Reason:     fmt.Sprintf("imported resource path %q is Edit=RequiresApproval; FieldEdit must carry plan-tied Approval audit metadata before apply (decision #42)", path),
			Suggestion: "set FieldEdit.Approval with approver, approved_at, and plan_hash bound to the reviewed plan",
		}, true
	}
	if iss, ok := approvalCompletenessIssue(issueField, path, edit.Approval); ok {
		return iss, true
	}
	return ValidationIssue{}, false
}

// evaluateChangeRisk returns an issue when the field's ChangeRisk implies a
// destroy/replace plan and the FieldEdit lacks a complete approval record
// (per decision #42, Unknown follows the MayReplace workflow). InPlace and
// the empty default fall through.
func evaluateChangeRisk(issueField, path string, edit imported.FieldEdit, entry policy.FieldPolicy) (ValidationIssue, bool) {
	if !requiresPlanConfirmation(entry.ChangeRisk) {
		return ValidationIssue{}, false
	}
	if edit.Approval == nil {
		return ValidationIssue{
			Field:      issueField,
			Value:      redactValue(entry, edit.NewValue),
			Code:       "imported_resource_field_edit_replacement_risk_unconfirmed",
			Reason:     fmt.Sprintf("imported resource path %q has ChangeRisk=%s; concrete Terraform plan review and explicit operator confirmation required before apply (decision #42)", path, displayChangeRisk(entry.ChangeRisk)),
			Suggestion: "review the plan, then set FieldEdit.Approval with the reviewed plan_hash",
		}, true
	}
	if iss, ok := approvalCompletenessIssue(issueField, path, edit.Approval); ok {
		return iss, true
	}
	return ValidationIssue{}, false
}

// approvalCompletenessIssue checks the four required Approval fields and
// returns imported_resource_field_edit_approval_invalid if any are missing.
// PlanHash is required so an approval cannot be silently reused across plans.
func approvalCompletenessIssue(issueField, path string, ap *imported.FieldEditApproval) (ValidationIssue, bool) {
	if ap == nil {
		return ValidationIssue{}, false
	}
	missing := []string{}
	if strings.TrimSpace(ap.Approver) == "" {
		missing = append(missing, "approver")
	}
	if ap.ApprovedAt.IsZero() {
		missing = append(missing, "approved_at")
	}
	if strings.TrimSpace(ap.PlanHash) == "" {
		missing = append(missing, "plan_hash")
	}
	if len(missing) == 0 {
		return ValidationIssue{}, false
	}
	return ValidationIssue{
		Field:      issueField,
		Code:       "imported_resource_field_edit_approval_invalid",
		Reason:     fmt.Sprintf("imported resource path %q FieldEdit.Approval is missing required field(s): %s", path, strings.Join(missing, ", ")),
		Suggestion: "populate every required Approval field before retrying",
	}, true
}

// evaluateReimportConflict checks whether the resource's desired Attributes
// value at path still matches the FieldEdit's NewValue. A divergence means a
// re-import (or other writer) clobbered the pending edit; per decision #19
// the operator must explicitly resolve before apply. Returns no issue when
// either side cannot be observed deterministically (nested paths, typed-only
// Attrs, or absent OldValue/NewValue) — the structural Phase 1 validators and
// The InsideOut backend's chat-stream loop carry the rest.
func evaluateReimportConflict(issueField, path string, edit imported.FieldEdit, ir imported.ImportedResource) (ValidationIssue, bool) {
	desired, ok := lookupTopLevelAttribute(ir.Attributes, path)
	if !ok {
		return ValidationIssue{}, false
	}
	if edit.NewValue == nil {
		return ValidationIssue{}, false
	}
	if reflect.DeepEqual(desired, edit.NewValue) {
		return ValidationIssue{}, false
	}
	return ValidationIssue{
		Field:      issueField,
		Code:       "imported_resource_field_edit_reimport_conflict",
		Reason:     fmt.Sprintf("imported resource path %q has a pending FieldEdit but desired state diverged from FieldEdit.NewValue; a re-import or other writer overwrote the edit (decision #19)", path),
		Suggestion: "operator must accept cloud, keep the edit, or abort",
	}, true
}

// lookupTopLevelAttribute returns the value at path in attrs when the path
// is a single segment with no dotted/bracketed sub-structure. Anything more
// complex (nested objects, JSON projections) returns ok=false; conflict
// detection conservatively no-ops on those paths in v1 rather than guessing.
func lookupTopLevelAttribute(attrs map[string]any, path string) (any, bool) {
	if len(attrs) == 0 {
		return nil, false
	}
	if strings.ContainsAny(path, ".[") {
		return nil, false
	}
	v, ok := attrs[path]
	return v, ok
}

// requiresPlanConfirmation reports whether c demands a plan-tied operator
// confirmation per decision #42. Only explicit MayReplace, AlwaysReplace, or
// Unknown trigger the gate; the empty default is treated as implicit InPlace
// per curator convention (every Phase 1 policy file leaves ChangeRisk unset
// on safely-edited tuning fields). Decision #42's "Unknown follows MayReplace"
// means an explicit Unknown gates apply, not that every uncurated field does.
func requiresPlanConfirmation(c policy.ChangeRiskPolicy) bool {
	switch c {
	case policy.ChangeMayReplace, policy.ChangeAlwaysReplace, policy.ChangeUnknown:
		return true
	}
	return false
}

// displayChangeRisk renders c for human-readable issue Reason strings.
func displayChangeRisk(c policy.ChangeRiskPolicy) string {
	return string(c)
}

// redactValue renders v for ValidationIssue.Value, replacing anything routed
// through a Sensitive or Redacted policy with "***" so raw sensitive data
// never escapes into errors, diffs, or chat correction prompts (decision #36).
func redactValue(p policy.FieldPolicy, v any) string {
	if v == nil {
		return ""
	}
	switch p.Sensitivity {
	case policy.SensitivitySensitive, policy.SensitivityRedacted:
		return "***"
	}
	return fmt.Sprintf("%v", v)
}

// sortedFieldEditPaths returns the keys of edits sorted lexicographically so
// the per-FieldEdit issue order is stable across runs (Go map iteration is
// otherwise randomized).
func sortedFieldEditPaths(edits map[string]imported.FieldEdit) []string {
	paths := make([]string, 0, len(edits))
	for p := range edits {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
