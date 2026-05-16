package policy

// awsApigatewayv2RoutePolicy curates Layer 2 for `aws_apigatewayv2_route`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An APIGW v2 Route binds a request (route_key like "GET /users") to an
// integration target. Identity is (api_id, id, route_key). Wiring axes
// are `target` (integration:<id>) and `authorizer_id`.
//
// Drift bundle 7 (#482): scalars use DriftSemanticExact;
// authorization_scopes is a list (WholeList). No tags surface.
var awsApigatewayv2RoutePolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"route_key": {
		// "<METHOD> <path>" — operator-controlled at create, route-key
		// changes destroy/recreate.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — API parent + integration target + authorizer -----------
	"api_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"target": {
		// "integrations/<integration_id>" — cross-resource pointer.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"authorizer_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Authorization + behavior -----------------------------------------
	"authorization_type": {
		// NONE | AWS_IAM | CUSTOM | JWT.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"authorization_scopes": {
		// JWT scopes required to invoke this route.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"api_key_required": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"operation_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"model_selection_expression": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"route_response_selection_expression": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_apigatewayv2_route", awsApigatewayv2RoutePolicy)
}
