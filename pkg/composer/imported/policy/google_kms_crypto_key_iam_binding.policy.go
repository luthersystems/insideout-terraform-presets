package policy

// googleKMSCryptoKeyIAMBindingPolicy curates the Layer 2 axes for the
// google_kms_crypto_key_iam_binding resource. The `members` list is a
// RoleIdentity multi-valued field; cumulatively the (crypto_key_id ×
// role) tuple is replacement-causing, but the members list itself is
// edited InPlace by the IAM policy (the provider PATCHes the binding).
var googleKMSCryptoKeyIAMBindingPolicy = Map{
	"id":            {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"etag":          {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"crypto_key_id": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"role":          {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"members":       {Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval, ChangeRisk: ChangeInPlace, DriftSemantic: DriftSemanticWholeList},
}

func init() {
	Register("google_kms_crypto_key_iam_binding", googleKMSCryptoKeyIAMBindingPolicy)
}
