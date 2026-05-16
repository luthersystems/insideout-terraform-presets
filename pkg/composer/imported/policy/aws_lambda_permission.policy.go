package policy

// awsLambdaPermissionPolicy curates Layer 2 for `aws_lambda_permission`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A `aws_lambda_permission` is one statement in a Lambda function's
// resource-policy granting an action (typically `lambda:InvokeFunction`)
// to a principal (a service principal like `apigateway.amazonaws.com`,
// an account ID, or an ARN). Identity is (statement_id, function_name).
// The whole tuple (action, principal, function_name, qualifier,
// source_account, source_arn) is pinned at create — flipping any of them
// is the security regression that drift detection wants to surface.
//
// Drift bundle 5 (#482): scalar attributes use DriftSemanticExact.
// There are no list-shaped attrs on this resource; the bundle-aggregate
// WholeList requirement is met by sibling policies (aws_cognito_user_
// pool_client, aws_ecs_service, etc.).
var awsLambdaPermissionPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"statement_id": {
		// The unique identifier of the policy statement. Identity for
		// the permission.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"statement_id_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — function the statement attaches to --------------------
	"function_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"qualifier": {
		// Alias name or version qualifier the statement applies to.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — action + principal + source restrictions --------------
	"action": {
		// Typically `lambda:InvokeFunction`. Identity-shaped.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"principal": {
		// Account ID / service principal / IAM ARN granted the action.
		// Security-critical — RequiresApproval gates edits.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"principal_org_id": {
		// AWS Organizations scoping condition.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"source_account": {
		// Tightens the principal to a specific source account.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"source_arn": {
		// Tightens the principal to a specific source resource ARN.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"event_source_token": {
		// Alexa-skill bearer token. Security-relevant.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"function_url_auth_type": {
		// AWS_IAM | NONE for Function URL-style invocations.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_lambda_permission", awsLambdaPermissionPolicy)
}
