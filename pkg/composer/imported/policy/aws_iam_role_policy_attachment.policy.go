package policy

// awsIAMRolePolicyAttachmentPolicy curates Layer 2 for
// `aws_iam_role_policy_attachment`.
//
// The TF resource binds one managed policy ARN to one IAM role —
// schema is two fields: `role` (role name) and `policy_arn`. Both are
// identity / wiring leaves with no editable surface (the resource
// represents a binding; editing means delete + recreate).
//
// Curation: `policy_arn` is the load-bearing wiring leaf — Exact
// equality on drift; the attachment vanishing out-of-band (operator
// detached the policy) is a real security event. The downstream
// inspector keys off the (role, policy_arn) tuple.
var awsIAMRolePolicyAttachmentPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"role": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — policy ARN reference ------------------------------------
	"policy_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_iam_role_policy_attachment", awsIAMRolePolicyAttachmentPolicy)
}
