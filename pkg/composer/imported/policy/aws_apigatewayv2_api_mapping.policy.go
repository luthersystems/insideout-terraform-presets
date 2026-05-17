package policy

// awsApigatewayv2APIMappingPolicy curates Layer 2 for
// `aws_apigatewayv2_api_mapping`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// An API mapping binds a custom `aws_apigatewayv2_domain_name` to a
// specific (api_id, stage), optionally under a path-prefix
// (api_mapping_key). Identity is (domain_name, id); the wiring axes are
// the API + stage pointers — retargeting one to a different API/stage
// silently swings public traffic onto a different backend, so drift on
// any of them is high-signal.
//
// Drift bundle 9 (#482): scalar attributes use DriftSemanticExact. No
// tag surface — API mappings are not directly tagged in the AWS APIGW v2
// data model.
var awsApigatewayv2APIMappingPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — domain + api + stage -----------------------------------
	"domain_name": {
		// Pointer to the parent custom domain (the public hostname
		// served by APIGW). Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"api_id": {
		// Pointer to the backing aws_apigatewayv2_api. Retargeting
		// to a different API swings public traffic — RequiresApproval.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"stage": {
		// Pointer to the stage of api_id that this mapping serves.
		// Retargeting flips which deployed version is exposed.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — path prefix ---------------------------------------------
	"api_mapping_key": {
		// Optional path prefix under the domain (empty = root). Visible
		// in the URL; ChatSafe so the agent can rearrange path layouts
		// without escalating.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_apigatewayv2_api_mapping", awsApigatewayv2APIMappingPolicy)
}
