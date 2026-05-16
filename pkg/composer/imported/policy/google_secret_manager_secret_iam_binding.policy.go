package policy

// googleSecretManagerSecretIAMBindingPolicy curates the Layer 2 axes
// for the google_secret_manager_secret_iam_binding resource. The
// `members` list is edited InPlace by the IAM policy PATCH; the
// (secret_id × role) tuple is identity.
//
// Drift bundle (#491): role is Exact; members is WholeList (mirrors
// the google_kms_crypto_key_iam_binding pattern — a missing-or-extra
// principal is a real security event surfaced as one mismatch).
// project + secret_id are Exact for completeness; id / etag stay
// DriftSemantic=None.
var googleSecretManagerSecretIAMBindingPolicy = Map{
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
	"members": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeInPlace,
		DriftSemantic: DriftSemanticWholeList,
	},
}

func init() {
	Register("google_secret_manager_secret_iam_binding", googleSecretManagerSecretIAMBindingPolicy)
}
