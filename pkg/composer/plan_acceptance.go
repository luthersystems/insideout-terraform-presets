package composer

import (
	"fmt"
	"reflect"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// ValidateFirstImportPlanOpts configures the first-import contract per
// docs/managed-resource-tiers.md decision #34: "First adoption apply:
// allowed actions are N to import, 0 to add, 0 to destroy, plus in-place
// additions/repairs of InsideOut provenance tags / labels."
type ValidateFirstImportPlanOpts struct {
	// ExpectedImports is the number of resources the plan should be
	// importing. A plan with a different count of Importing changes
	// (counting both ResourceChanges and ResourceDrift) fails.
	ExpectedImports int

	// ProvenanceLabelKeys lists the leaf attribute paths that may be
	// added/repaired alongside the imports without failing the contract.
	// Use FirstImportProvenanceKeys(cloud) for the canonical set.
	ProvenanceLabelKeys []string
}

// ValidateSubsequentApplyPlanOpts configures the looser contract for
// re-applies per decision #34: "Subsequent applies: allowed actions are
// exactly the user-approved desired-state changes plus provenance tag /
// label repair."
type ValidateSubsequentApplyPlanOpts struct {
	// ProvenanceLabelKeys lists the leaf attribute paths that may be
	// added/repaired without an approval. Use
	// FirstImportProvenanceKeys(cloud) for the canonical set.
	ProvenanceLabelKeys []string
}

// FirstImportProvenanceKeys returns the canonical set of provenance
// tag/label leaf paths for cloud ∈ {"aws", "gcp"}. These are the only
// non-import writes the first-import contract permits on each resource;
// callers pass the returned slice as
// ValidateFirstImportPlanOpts.ProvenanceLabelKeys.
//
// The paths include both the user-facing attribute (tags / labels) and
// the provider-computed mirror attributes (tags_all on AWS;
// effective_labels and terraform_labels on GCP) because a single
// provenance write surfaces as a diff in all of them.
func FirstImportProvenanceKeys(cloud string) []string {
	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "aws":
		var out []string
		for _, parent := range []string{"tags", "tags_all"} {
			for _, k := range []string{
				AWSTagKeyImportProject, AWSTagKeyImportSession,
				AWSTagKeyImported, AWSTagKeyImportedAt,
			} {
				out = append(out, parent+"."+k)
			}
		}
		return out
	case "gcp":
		var out []string
		for _, parent := range []string{"labels", "effective_labels", "terraform_labels"} {
			for _, k := range []string{
				GCPLabelKeyImportProject, GCPLabelKeyImportSession,
				GCPLabelKeyImported, GCPLabelKeyImportedAt,
			} {
				out = append(out, parent+"."+k)
			}
		}
		return out
	}
	return nil
}

// ValidateFirstImportPlan asserts the first-import contract on plan
// (decision #34). Returns nil on pass; a []ValidationIssue with one or
// more entries on fail. Issue codes:
//
//   - imported_plan_unexpected_import_count — plan imports a different
//     number of resources than opts.ExpectedImports.
//   - imported_plan_unexpected_create — a non-import resource is being
//     created.
//   - imported_plan_unexpected_destroy — a resource is being destroyed.
//   - imported_plan_unexpected_replace — a resource is being replaced.
//   - imported_plan_unauthorized_change — a resource has an update whose
//     diff includes attribute paths outside opts.ProvenanceLabelKeys.
//
// A nil plan returns one issue with code imported_plan_nil_input.
func ValidateFirstImportPlan(plan *tfjson.Plan, opts ValidateFirstImportPlanOpts) []ValidationIssue {
	if plan == nil {
		return []ValidationIssue{{
			Field:  "plan",
			Code:   "imported_plan_nil_input",
			Reason: "plan is nil; cannot validate first-import contract",
		}}
	}

	var issues []ValidationIssue
	importCount := 0

	for _, rc := range allResourceChanges(plan) {
		if rc == nil || rc.Change == nil {
			continue
		}
		field := planChangeField(rc)
		actions := rc.Change.Actions
		importing := rc.Change.Importing != nil

		if importing {
			importCount++
			// An import may carry a side-effect update for provenance
			// tags/labels. Any non-provenance attribute change fails.
			if actions.Update() || actions.Replace() {
				issues = append(issues,
					unauthorizedChangeIssues(field, rc.Type, rc.Change, opts.ProvenanceLabelKeys)...)
			}
			continue
		}

		switch {
		case actions.NoOp(), actions.Read():
			// No-ops and reads are always safe.
		case actions.Replace():
			issues = append(issues, ValidationIssue{
				Field:  field,
				Value:  joinActions(actions),
				Code:   "imported_plan_unexpected_replace",
				Reason: fmt.Sprintf("first-import plan must not replace resources; %q has actions %v", rc.Address, actions),
			})
		case actions.Delete():
			issues = append(issues, ValidationIssue{
				Field:  field,
				Value:  joinActions(actions),
				Code:   "imported_plan_unexpected_destroy",
				Reason: fmt.Sprintf("first-import plan must not destroy resources; %q has actions %v", rc.Address, actions),
			})
		case actions.Create():
			issues = append(issues, ValidationIssue{
				Field:  field,
				Value:  joinActions(actions),
				Code:   "imported_plan_unexpected_create",
				Reason: fmt.Sprintf("first-import plan must not create resources; %q has actions %v", rc.Address, actions),
			})
		case actions.Update():
			// A non-import update is only allowed if every changed
			// attribute is in the provenance allowlist.
			issues = append(issues,
				unauthorizedChangeIssues(field, rc.Type, rc.Change, opts.ProvenanceLabelKeys)...)
		}
	}

	if importCount != opts.ExpectedImports {
		issues = append(issues, ValidationIssue{
			Field:  "plan.imports",
			Value:  fmt.Sprintf("%d", importCount),
			Code:   "imported_plan_unexpected_import_count",
			Reason: fmt.Sprintf("first-import plan expected %d imports, got %d", opts.ExpectedImports, importCount),
		})
	}

	return issues
}

// ValidateSubsequentApplyPlan asserts the subsequent-apply contract
// (decision #34): every resource-change must be either a no-op, an
// import {} no-op (the import block survived; the address is already in
// state), a provenance-only write, or covered by a matching
// FieldEditApproval on the corresponding ImportedResource's FieldEdits.
//
// Issue codes:
//
//   - imported_plan_unapproved_replace — replace not covered by an
//     approval on the matching ImportedResource.
//   - imported_plan_unapproved_destroy — destroy not covered by an
//     approval.
//   - imported_plan_unapproved_create — create of a resource that has no
//     corresponding ImportedResource record.
//   - imported_plan_unauthorized_change — update touching attribute
//     paths that are neither provenance keys nor present in the
//     ImportedResource's approved FieldEdits set.
//
// A nil plan returns one issue with code imported_plan_nil_input.
func ValidateSubsequentApplyPlan(plan *tfjson.Plan, irs []imported.ImportedResource, opts ValidateSubsequentApplyPlanOpts) []ValidationIssue {
	if plan == nil {
		return []ValidationIssue{{
			Field:  "plan",
			Code:   "imported_plan_nil_input",
			Reason: "plan is nil; cannot validate subsequent-apply contract",
		}}
	}

	// Index approved paths by Terraform address.
	approvedByAddr := map[string]map[string]struct{}{}
	for _, ir := range irs {
		if ir.Identity.Address == "" || len(ir.FieldEdits) == 0 {
			continue
		}
		paths := map[string]struct{}{}
		for path, edit := range ir.FieldEdits {
			if edit.Approval != nil {
				paths[path] = struct{}{}
			}
		}
		if len(paths) > 0 {
			approvedByAddr[ir.Identity.Address] = paths
		}
	}

	var issues []ValidationIssue
	for _, rc := range plan.ResourceChanges {
		if rc == nil || rc.Change == nil {
			continue
		}
		field := planChangeField(rc)
		actions := rc.Change.Actions
		importing := rc.Change.Importing != nil

		// Pure no-op import (the import {} block survived a re-apply
		// once the address is already in state) is the documented
		// expected shape.
		if importing && actions.NoOp() {
			continue
		}

		switch {
		case actions.NoOp(), actions.Read():
			// Safe.
		case actions.Replace():
			if !changeCoveredByApproval(rc, approvedByAddr) {
				issues = append(issues, ValidationIssue{
					Field:  field,
					Value:  joinActions(actions),
					Code:   "imported_plan_unapproved_replace",
					Reason: fmt.Sprintf("replace of %q is not covered by a FieldEditApproval; subsequent-apply contract requires explicit operator approval for replace", rc.Address),
				})
			}
		case actions.Delete():
			if !changeCoveredByApproval(rc, approvedByAddr) {
				issues = append(issues, ValidationIssue{
					Field:  field,
					Value:  joinActions(actions),
					Code:   "imported_plan_unapproved_destroy",
					Reason: fmt.Sprintf("destroy of %q is not covered by a FieldEditApproval; subsequent-apply contract requires explicit operator approval for destroy", rc.Address),
				})
			}
		case actions.Create():
			if _, ok := approvedByAddr[rc.Address]; !ok {
				issues = append(issues, ValidationIssue{
					Field:  field,
					Value:  joinActions(actions),
					Code:   "imported_plan_unapproved_create",
					Reason: fmt.Sprintf("create of %q is not covered by a matching ImportedResource record; subsequent-apply contract limits creates to approved desired-state changes", rc.Address),
				})
			}
		case actions.Update():
			allowed := approvedByAddr[rc.Address]
			issues = append(issues,
				unapprovedChangeIssues(field, rc.Change, opts.ProvenanceLabelKeys, allowed)...)
		}
	}

	return issues
}

// unauthorizedChangeIssues returns one issue per leaf attribute path in
// change.Before vs change.After that is not in allowedKeys. Used by the
// first-import contract where the only permitted updates are provenance
// repair.
//
// Tag/label maps (AWS `tags`/`tags_all`, GCP `labels`/`effective_labels`/
// `terraform_labels`) get special map-aware treatment: a leaf-key diff is
// authorized iff every added key is in allowedKeys (i.e. a provenance
// marker) and no pre-existing key is removed or modified. Pre-existing
// keys that only appear in `after` because the before-side of the
// computed mirror was nil/empty (the canonical `tags_all` shape on a
// fresh import) are treated as discovered state, not as additions. This
// closes the false-positive described in issue #685 where a clean
// tag-only first-import plan got flagged as unauthorized drift purely
// because `tags_all.Before` was nil while `tags_all.After` carried the
// full user-tag union.
func unauthorizedChangeIssues(field, resourceType string, change *tfjson.Change, allowedKeys []string) []ValidationIssue {
	allow := stringSet(allowedKeys)
	tagParents := tagMapParents(allowedKeys)
	bad := diffPathsExcludingTagMaps(change.Before, change.After, "", tagParents)

	var issues []ValidationIssue
	for _, p := range bad {
		if _, ok := allow[p]; ok {
			continue
		}
		if allowedFirstImportOptionalZeroDefault(resourceType, p, change.Before, change.After) {
			continue
		}
		if isFirstImportBehavioralAttr(resourceType, p) {
			continue
		}
		issues = append(issues, ValidationIssue{
			Field:  field + "." + p,
			Code:   "imported_plan_unauthorized_change",
			Reason: fmt.Sprintf("first-import plan must only repair provenance tags/labels; path %q is not in the allowed set", p),
		})
	}

	// Now run the tag-map-aware check for each known parent.
	for parent := range tagParents {
		issues = append(issues, tagMapIssues(field, parent, change.Before, change.After, allow)...)
	}

	return issues
}

func allowedFirstImportOptionalZeroDefault(resourceType, path string, before, after any) bool {
	_, schema, ok := generated.Lookup(resourceType)
	if !ok {
		return false
	}
	field, ok := schema[path]
	if !ok || !field.Optional || field.Required {
		return false
	}
	beforeValue, beforeOK := valueAtPath(before, path)
	if beforeOK && beforeValue != nil {
		return false
	}
	afterValue, afterOK := valueAtPath(after, path)
	return afterOK && isZeroProviderDefault(afterValue)
}

// firstImportBehavioralAttrs is a hand-curated allowlist of
// (resourceType -> attribute-path) pairs that are DEPLOY-DIRECTIVE /
// WRITE-ONLY / NON-ROUND-TRIPPABLE attributes. Their value in the
// generated config is an instruction to the provider at apply time, NOT
// a faithful read-back of remote state, so a first-import `terraform
// plan` legitimately shows an in-place diff on them even though nothing
// real has drifted. The first-import contract (decision #34) otherwise
// only permits provenance tag/label repair, which would reject these
// benign diffs and fail the whole-account import (job ends `partial`).
//
// This is intentionally distinct from allowedFirstImportOptionalZeroDefault,
// which only forgives the narrow null-before -> zero-provider-default-after
// shape. A behavioral attr may diff to a NON-zero value (e.g.
// aws_cloudfront_function.publish defaults to true and round-trips as a
// true->true or null->true first-plan diff), so the zero-default helper
// cannot cover it.
//
// CONSERVATIVE BY DESIGN: only attributes that are unambiguously a deploy
// directive or a provider write-only field belong here. A normal config
// attribute that should faithfully match remote state must NEVER be added
// — doing so would silently mask real drift on first import. Each entry
// is justified inline; when in doubt, leave it out.
//
// Triggering example: issue #833 — whole-account reverse-import of account
// 141812438321 (staging session sess_v2_fvZSf5IfhLCb, job ri-897a6c4e-kff6g)
// failed at `terraform plan` because 4 aws_cloudfront_function resources
// (e.g. aws_cloudfront_function.a2ae0703_ln_default_luther_api_cf_site157d)
// produced an `imported_plan_unauthorized_change` on path "publish".
var firstImportBehavioralAttrs = map[string]map[string]struct{}{
	// `publish` controls whether the provider publishes the LIVE stage on
	// apply. It defaults to true and is not a faithful read-back of remote
	// state, so genconfig's emitted value yields a benign first-plan diff.
	// (#833 — the exact prod-failing case.)
	"aws_cloudfront_function": {
		"publish": {},
	},
	// `publish` is a deploy directive: when true, the provider publishes a
	// new immutable Lambda version on apply. It is an apply-time action,
	// not round-trippable readback state. The narrow null->false shape was
	// already forgiven by allowedFirstImportOptionalZeroDefault; this
	// covers the remaining behavioral shapes (e.g. genconfig emitting true,
	// or a function with a published version) for class consistency (#833).
	"aws_lambda_function": {
		"publish": {},
	},
	// `publish` is a deploy directive: when true, the provider publishes a
	// new state-machine version on apply. Same write-only/apply-time
	// semantics as the Lambda case (#833 sweep).
	"aws_sfn_state_machine": {
		"publish": {},
	},
}

// isFirstImportBehavioralAttr reports whether path on resourceType is a
// curated deploy-directive / write-only attribute that the first-import
// contract permits to differ. See firstImportBehavioralAttrs.
func isFirstImportBehavioralAttr(resourceType, path string) bool {
	paths, ok := firstImportBehavioralAttrs[resourceType]
	if !ok {
		return false
	}
	_, ok = paths[path]
	return ok
}

func isZeroProviderDefault(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == ""
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

func valueAtPath(v any, path string) (any, bool) {
	cur := v
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// unapprovedChangeIssues is the subsequent-apply variant: leaf paths
// that are in neither the provenance allowlist nor the
// per-ImportedResource approved-edit set fail.
func unapprovedChangeIssues(field string, change *tfjson.Change, allowedProvenance []string, approvedPaths map[string]struct{}) []ValidationIssue {
	allow := stringSet(allowedProvenance)
	bad := diffPaths(change.Before, change.After, "")
	var issues []ValidationIssue
	for _, p := range bad {
		if _, ok := allow[p]; ok {
			continue
		}
		if _, ok := approvedPaths[p]; ok {
			continue
		}
		issues = append(issues, ValidationIssue{
			Field:  field + "." + p,
			Code:   "imported_plan_unauthorized_change",
			Reason: fmt.Sprintf("attribute change at %q on this resource is neither a provenance repair nor covered by an approved FieldEdit", p),
		})
	}
	return issues
}

// changeCoveredByApproval reports whether the resource at rc.Address has
// any approved field edit. The plan-side check is intentionally coarse
// — we don't try to map a replace's ReplacePaths to specific edits,
// since the ChangeRisk-driven approval already happens in
// ValidateImportedResourceAuthorization. The plan-side gate just
// confirms that an approval exists at all.
func changeCoveredByApproval(rc *tfjson.ResourceChange, approvedByAddr map[string]map[string]struct{}) bool {
	paths, ok := approvedByAddr[rc.Address]
	return ok && len(paths) > 0
}

// diffPaths returns the dotted leaf paths where before != after.
// Treats nil and a missing key uniformly. List values are compared by
// reflect.DeepEqual at the slice level (no element-wise descent), since
// a re-ordered list at the JSON level always indicates a real change to
// the provider.
func diffPaths(before, after any, prefix string) []string {
	return diffPathsExcludingTagMaps(before, after, prefix, nil)
}

// diffPathsExcludingTagMaps is diffPaths plus an "exclude these top-level
// keys" filter so the caller can handle tag/label parent maps with
// map-aware semantics (see tagMapIssues). When skip is empty the
// behaviour is identical to diffPaths.
func diffPathsExcludingTagMaps(before, after any, prefix string, skip map[string]struct{}) []string {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	bMap, bIsMap := before.(map[string]any)
	aMap, aIsMap := after.(map[string]any)
	if bIsMap || aIsMap {
		var paths []string
		keys := unionKeys(bMap, aMap)
		for _, k := range keys {
			child := k
			if prefix != "" {
				child = prefix + "." + k
			}
			// Only suppress at the top level — a key called `tags`
			// nested inside another block is not a provenance parent.
			if prefix == "" {
				if _, ok := skip[k]; ok {
					continue
				}
			}
			paths = append(paths, diffPathsExcludingTagMaps(bMap[k], aMap[k], child, skip)...)
		}
		return paths
	}
	if prefix == "" {
		return []string{"<root>"}
	}
	return []string{prefix}
}

// tagMapParents returns the set of top-level attribute names that the
// caller passed as provenance-allowed parents. Derived from allowedKeys
// (paths of the form `<parent>.<key>`) so the validator stays driven by
// the FirstImportProvenanceKeys contract — adding a new cloud's tag
// parent only requires updating that one builder.
func tagMapParents(allowedKeys []string) map[string]struct{} {
	parents := make(map[string]struct{})
	for _, p := range allowedKeys {
		idx := strings.Index(p, ".")
		if idx <= 0 {
			continue
		}
		parents[p[:idx]] = struct{}{}
	}
	return parents
}

// tagMapIssues validates a single tag/label parent map (e.g. `tags`,
// `tags_all`, `labels`, `effective_labels`, `terraform_labels`) with
// map-aware semantics:
//
//   - Pre-existing keys removed or modified → unauthorized change. The
//     first-import contract forbids any user-tag touch beyond provenance
//     writes.
//   - Keys added when the before-side was present (even as `{}`): only
//     allowed if the leaf path is in the provenance allowlist. An
//     explicit empty map means the user genuinely had no tags, so any
//     non-provenance addition is a real user-tag write.
//   - Keys added when the before-side was NIL (the value was absent or
//     literally null in the plan JSON): treated as "discovered state,
//     not an InsideOut write". This is the canonical shape for AWS
//     `tags_all` on a fresh import — terraform's pre-refresh state has
//     it as null while the post-refresh `after` shows the union of user
//     tags + provider defaults. Suppressing the noise here is the fix
//     for issue #685.
//
// Values that aren't maps on both sides (e.g. one side is nil, both
// scalar) are handled by the nil-before exception or by reporting the
// leaf path as a single unauthorized change.
func tagMapIssues(field, parent string, before, after any, allow map[string]struct{}) []ValidationIssue {
	bMap, beforePresent := mapAtKey(before, parent)
	aMap, _ := mapAtKey(after, parent)
	if reflect.DeepEqual(bMap, aMap) {
		return nil
	}
	var issues []ValidationIssue
	emit := func(key, suffix string) {
		path := parent + "." + key
		issues = append(issues, ValidationIssue{
			Field:  field + "." + path,
			Code:   "imported_plan_unauthorized_change",
			Reason: fmt.Sprintf("first-import plan must only repair provenance tags/labels; %s of %q is not permitted", suffix, path),
		})
	}
	for _, k := range unionKeys(bMap, aMap) {
		bVal, bHas := bMap[k]
		aVal, aHas := aMap[k]
		switch {
		case bHas && aHas:
			if !reflect.DeepEqual(bVal, aVal) {
				emit(k, "modification")
			}
		case bHas && !aHas:
			emit(k, "removal")
		case !bHas && aHas:
			if _, allowed := allow[parent+"."+k]; allowed {
				continue
			}
			// Suppress discovered keys when the before-side of the
			// parent was nil — covers the canonical fresh-import shape
			// for computed mirrors (AWS `tags_all`, GCP
			// `effective_labels`) where state has them as null until
			// after the first refresh. A present-but-empty `{}` before
			// is treated as user intent (no tags) so non-provenance
			// adds still fail there.
			if !beforePresent {
				continue
			}
			emit(k, "addition")
		}
	}
	return issues
}

// mapAtKey returns the map value at obj[key] when obj is a map. The
// second return reports whether the key existed in the parent at all
// (even as nil / non-map). Callers use that to distinguish "absent /
// computed-mirror not yet refreshed" from "explicitly empty map".
func mapAtKey(obj any, key string) (map[string]any, bool) {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil, false
	}
	v, present := m[key]
	if !present {
		return nil, false
	}
	if v == nil {
		return nil, false
	}
	mv, _ := v.(map[string]any)
	return mv, true
}

func stringSet(xs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		out[x] = struct{}{}
	}
	return out
}

// allResourceChanges concatenates ResourceChanges and ResourceDrift so
// the first-import counter doesn't miss an import that surfaced as
// pre-plan drift on a re-run.
func allResourceChanges(plan *tfjson.Plan) []*tfjson.ResourceChange {
	out := make([]*tfjson.ResourceChange, 0, len(plan.ResourceChanges)+len(plan.ResourceDrift))
	out = append(out, plan.ResourceChanges...)
	out = append(out, plan.ResourceDrift...)
	return out
}

// planChangeField builds a ValidationIssue.Field path for a plan
// resource-change, mirroring imported_validate.go's importedField shape
// for consistency with the rest of the family.
func planChangeField(rc *tfjson.ResourceChange) string {
	if rc.Address == "" {
		return "plan.resource_changes"
	}
	return "plan." + rc.Address
}

func joinActions(actions tfjson.Actions) string {
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		parts = append(parts, string(a))
	}
	return strings.Join(parts, ",")
}
