package policy

// googleProjectIAMMemberPolicy curates the Layer 2 axes for the
// google_project_iam_member resource. IAM-binding resources are
// minimally curated by design — every field is either an immutable
// identity component (project, role, member) or a TF-internal
// computed scalar (id, etag). The conditional binding sub-block is
// deliberately omitted: the discoverer flattens conditional bindings
// into separate rows, so a condition block surfaces as additional
// rows rather than as an editable nested struct here.
//
// Drift bundle (#491): role + member are Exact — an out-of-band IAM
// edit that flips either is a project-scope security event. project
// is also Exact for completeness. id / etag stay DriftSemantic=None.
var googleProjectIAMMemberPolicy = Map{
	// Identity.
	"id":   {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"etag": {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"role": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"member": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("google_project_iam_member", googleProjectIAMMemberPolicy)
}
