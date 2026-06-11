package policy

// awsSagemakerEndpointPolicy curates Layer 2 for
// `aws_sagemaker_endpoint`.
//
// #787 backfill for the SageMaker real-time inference triad (#761). An
// endpoint is the served surface; its only mutable binding is
// endpoint_config_name, which points at the immutable endpoint
// configuration that pins the model + capacity. A silent rebind to a
// different config is exactly what "production quietly serves a
// different model / scale" looks like — DriftSemanticExact + Reliability
// pillar.
//
// The deployment_config block is a blue/green / rolling update strategy
// that only governs how a config swap is rolled out; it is operational
// tuning, left uncurated below the curated binding to keep the policy
// focused on the drift-meaningful surface.
var awsSagemakerEndpointPolicy = Map{
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
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — the served configuration ------------------------------
	"endpoint_config_name": {
		// Silent rebind serves a different model / capacity in production.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_sagemaker_endpoint", awsSagemakerEndpointPolicy)
}
