package policy

// googleSecretManagerSecretIAMMemberPolicy curates the Layer 2 axes
// for the google_secret_manager_secret_iam_member resource. The
// `secret_id` field is treated as identity even though it appears as
// a top-level attribute on this child resource — identityAttrLeaves
// names it explicitly.
//
// Drift bundle (#491): role + member are Exact — an out-of-band IAM
// edit that flips either is a real security event. project +
// secret_id are Exact for completeness. id / etag stay
// DriftSemantic=None.
var googleSecretManagerSecretIAMMemberPolicy = Map{
	"id":   {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"etag": {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"secret_id": {
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
	Register("google_secret_manager_secret_iam_member", googleSecretManagerSecretIAMMemberPolicy)
}
