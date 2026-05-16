package policy

// googleSecretManagerSecretIAMMemberPolicy curates the Layer 2 axes
// for the google_secret_manager_secret_iam_member resource. The
// `secret_id` field is treated as identity even though it appears as
// a top-level attribute on this child resource — identityAttrLeaves
// names it explicitly.
var googleSecretManagerSecretIAMMemberPolicy = Map{
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"etag":      {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project":   {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"secret_id": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"role":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"member":    {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
}

func init() {
	Register("google_secret_manager_secret_iam_member", googleSecretManagerSecretIAMMemberPolicy)
}
