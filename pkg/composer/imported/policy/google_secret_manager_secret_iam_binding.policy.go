package policy

// googleSecretManagerSecretIAMBindingPolicy curates the Layer 2 axes
// for the google_secret_manager_secret_iam_binding resource. The
// `members` list is edited InPlace by the IAM policy PATCH; the
// (secret_id × role) tuple is identity.
var googleSecretManagerSecretIAMBindingPolicy = Map{
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"etag":      {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project":   {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"secret_id": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"role":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"members":   {Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval, ChangeRisk: ChangeInPlace, DriftSemantic: DriftSemanticWholeList},
}

func init() {
	Register("google_secret_manager_secret_iam_binding", googleSecretManagerSecretIAMBindingPolicy)
}
