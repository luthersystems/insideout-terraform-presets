package policy

// awsApigatewayv2APIPolicy curates Layer 2 for `aws_apigatewayv2_api`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An APIGW v2 API is the top-level container for HTTP / WebSocket APIs.
// Identity is (id, arn, name). Protocol-type pins the API shape at
// create. Wiring axes are `body` (OpenAPI spec) and the public-endpoint
// kill-switch `disable_execute_api_endpoint`.
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact. The nested
// `cors_configuration` block is left uncurated — block-level drift is a
// follow-up. Tags use tagPolicy().
var awsApigatewayv2APIPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"execution_arn": {
		// Stable ARN used by Lambda integration resource policies.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"api_endpoint": {
		// Provider-allocated default endpoint hostname.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Protocol + behavior pinned at create ----------------------------
	"protocol_type": {
		// HTTP | WEBSOCKET. Pinned at create.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"route_selection_expression": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"api_key_selection_expression": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"disable_execute_api_endpoint": {
		// When true, the default execute-api.<region>.amazonaws.com URL
		// is disabled — clients must use the custom domain.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"fail_on_warnings": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// OpenAPI spec import ---------------------------------------------
	"body": {
		// OpenAPI 3.x JSON / YAML import body. Opaque blob — exact match
		// flags any out-of-band edit to the API surface.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"credentials_arn": {
		// IAM role assumed during import for OpenAPI-referenced resources.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"route_key": {
		// Quick-create route binding (paired with `target`); identity-ish
		// in quick-create form.
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"target": {
		// Quick-create integration URI.
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_apigatewayv2_api", awsApigatewayv2APIPolicy)
}
