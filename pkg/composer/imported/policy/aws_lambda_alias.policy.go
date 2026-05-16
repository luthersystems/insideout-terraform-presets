package policy

// awsLambdaAliasPolicy curates Layer 2 for `aws_lambda_alias`. Cloud-
// control-routed enrichment already produces typed Attrs; this map adds
// the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Lambda alias is a named pointer to a function version (e.g. PROD ->
// version 5) with optional weighted routing to a second version (the
// CodeDeploy blue/green primitive). Identity is (id, name, arn,
// function_name). The wiring is (function_version, routing_config); the
// `routing_config.additional_version_weights` map is the per-deploy
// shifting weight surface.
//
// Drift bundle 5 (#482): scalar attributes use DriftSemanticExact;
// routing_config is a nested-list-of-one whose
// additional_version_weights map is treated WholeList at the routing_config
// block level because a missing or extra version-weight pair is the
// shape of the diff that matters operationally.
var awsLambdaAliasPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"invoke_arn": {
		// API Gateway integration target ARN. Computed.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — function pointer (alias name → version) ---------------
	"function_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"function_version": {
		// The primary version the alias points at. Operationally edited
		// by deploy tooling; treat as RelationshipOnly so the graph
		// resolver owns it.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — description + routing_config ---------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"routing_config": {
		// Blue/green weighted-shift block — list-of-one. Whole-list
		// compare so any change in the shift weights is one diff entry.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
}

func init() {
	Register("aws_lambda_alias", awsLambdaAliasPolicy)
}
