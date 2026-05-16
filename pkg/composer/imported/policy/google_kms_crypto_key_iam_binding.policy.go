package policy

// googleKMSCryptoKeyIAMBindingPolicy curates the Layer 2 axes for the
// google_kms_crypto_key_iam_binding resource. The `members` list is a
// RoleIdentity multi-valued field; cumulatively the (crypto_key_id ×
// role) tuple is replacement-causing, but the members list itself is
// edited InPlace by the IAM policy (the provider PATCHes the binding).
//
// Drift bundle (#491): role is Exact; members is WholeList (order-
// insensitive set semantics, but a missing-or-extra principal is a
// real security event that must surface as one mismatch rather than
// per-element noise — mirrors the aws_iam_role.managed_policy_arns
// pattern). crypto_key_id is Exact for completeness; id / etag stay
// DriftSemantic=None.
var googleKMSCryptoKeyIAMBindingPolicy = Map{
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"etag": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"crypto_key_id": {
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
	Register("google_kms_crypto_key_iam_binding", googleKMSCryptoKeyIAMBindingPolicy)
}
