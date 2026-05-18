package policy

// awsAPIGatewayDeploymentPolicy curates Layer 2 for
// `aws_api_gateway_deployment`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// An APIGW v1 deployment is the immutable snapshot a `stage` points at.
// Identity is (rest_api_id, id); description and triggers are the
// edit-relevant axes — `triggers` is the hash-based trigger map that
// forces a new deployment when the upstream config changes.
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact. No tag
// surface on this resource.
var awsAPIGatewayDeploymentPolicy = Map{
	// Identity ----------------------------------------------------------
	// AWS provider 6.x removed the `execution_arn` computed attribute from
	// aws_api_gateway_deployment (it lives on aws_api_gateway_stage now —
	// the deployment itself is API-Gateway-internal and doesn't have a
	// per-resource invocation URL). #599 schema-bump cleanup.
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent REST API ----------------------------------------
	"rest_api_id": {
		// Pointer to the parent aws_api_gateway_rest_api. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — description + invalidation triggers --------------------
	"description": {
		// Human-readable deployment description; safe to update.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_api_gateway_deployment", awsAPIGatewayDeploymentPolicy)
}
