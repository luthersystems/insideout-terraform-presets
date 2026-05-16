package policy

// awsApigatewayv2AuthorizerPolicy curates Layer 2 for
// `aws_apigatewayv2_authorizer`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// An APIGW v2 Authorizer is a JWT or Lambda authorizer attached to an
// HTTP / WebSocket API. Identity is (api_id, id, name). Wiring axes are
// `authorizer_uri` (target Lambda) and `authorizer_credentials_arn` (the
// IAM role API Gateway assumes to invoke the Lambda).
//
// Drift bundle 7 (#482): scalars use DriftSemanticExact;
// identity_sources is a list, marked DriftSemanticWholeList. No tags
// surface (authorizers are untaggable).
var awsApigatewayv2AuthorizerPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Authorizer logical name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — API parent + Lambda target + IAM credentials -----------
	"api_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"authorizer_uri": {
		// Lambda function URI the authorizer invokes. Cross-resource.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"authorizer_credentials_arn": {
		// IAM role ARN API Gateway uses to invoke the authorizer Lambda.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Authorizer type + behavior ---------------------------------------
	"authorizer_type": {
		// JWT | REQUEST. Pinned at create.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"authorizer_payload_format_version": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"authorizer_result_ttl_in_seconds": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_simple_responses": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"identity_sources": {
		// Request fields the authorizer inspects (e.g. $request.header.Authorization).
		// Order is semantically meaningful — provider treats as a set but
		// the comparator pins exact list equality to flag silent edits.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// timeouts singleton ------------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_apigatewayv2_authorizer", awsApigatewayv2AuthorizerPolicy)
}
