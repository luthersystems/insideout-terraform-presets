package policy

// awsApigatewayv2IntegrationPolicy curates Layer 2 for
// `aws_apigatewayv2_integration`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// An APIGW v2 Integration is the backend target a route forwards to:
// Lambda, HTTP, VPC link, or AWS_PROXY for direct service calls.
// Identity is (api_id, id). Wiring axes are `integration_uri` (the
// target backend), `connection_id` (VPC link), and `credentials_arn`
// (IAM role API Gateway assumes when invoking the backend).
//
// Drift bundle 7 (#482): scalars use DriftSemanticExact. No tags surface
// (integrations are untaggable).
var awsApigatewayv2IntegrationPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — API parent + backend target + IAM credentials ----------
	"api_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"integration_uri": {
		// Lambda invoke ARN or backend HTTP URL.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"connection_id": {
		// VPC link ID for private integrations.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"credentials_arn": {
		// IAM role ARN API Gateway assumes when invoking the backend.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Integration type / method / behavior -----------------------------
	"integration_type": {
		// AWS | AWS_PROXY | HTTP | HTTP_PROXY | MOCK | VPC_LINK. Pinned at create.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"integration_subtype": {
		// AWS service action for direct AWS_PROXY integrations.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"integration_method": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"connection_type": {
		// INTERNET | VPC_LINK.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"content_handling_strategy": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"passthrough_behavior": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"payload_format_version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"timeout_milliseconds": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_apigatewayv2_integration", awsApigatewayv2IntegrationPolicy)
}
