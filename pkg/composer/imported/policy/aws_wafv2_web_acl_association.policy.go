package policy

// awsWafv2WebACLAssociationPolicy curates Layer 2 for
// `aws_wafv2_web_acl_association`.
//
// The TF resource binds one regional resource ARN (ALB, API Gateway
// stage, AppSync GraphQL API, Cognito user pool, App Runner service,
// Verified Access instance, etc.) to one WAFv2 Web ACL ARN. Schema is
// two leaves — both identity / wiring leaves with no editable surface
// (replacing the binding means delete + recreate).
//
// Curation: `web_acl_arn` is the load-bearing wiring leaf — Exact
// equality on drift; the association vanishing out-of-band (someone
// detached the WebACL) is a real security event. The downstream
// inspector keys off the (resource_arn, web_acl_arn) tuple.
var awsWafv2WebACLAssociationPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"resource_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — Web ACL reference ---------------------------------------
	"web_acl_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_wafv2_web_acl_association", awsWafv2WebACLAssociationPolicy)
}
