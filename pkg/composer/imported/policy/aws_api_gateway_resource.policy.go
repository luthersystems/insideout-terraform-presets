package policy

// awsAPIGatewayResourcePolicy curates Layer 2 for `aws_api_gateway_resource`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An API Gateway Resource is a node in the REST API URL tree. Identity is
// (rest_api_id, id, path); the parent_id wires the resource into the tree.
// path_part and parent_id are AlwaysReplace — moving a node in the tree
// requires destroy/recreate at the provider level.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact. This
// type has no list-shaped attributes; the WholeList axis is intentionally
// unused (mirrors aws_apigatewayv2_stage, aws_iam_policy, etc.).
var awsAPIGatewayResourcePolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"path": {
		// Computed full URL path — identity in the REST API URL tree.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"path_part": {
		// The trailing path segment under parent_id. Renaming the segment
		// destroys and recreates the resource (and any methods below it).
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — REST API parent + tree position --------------------------
	"rest_api_id": {
		// Pointer to the owning aws_api_gateway_rest_api. Cross-resource.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"parent_id": {
		// Pointer to the parent Resource (or REST API root resource).
		// Moving a resource to a new parent forces replacement.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_api_gateway_resource", awsAPIGatewayResourcePolicy)
}
