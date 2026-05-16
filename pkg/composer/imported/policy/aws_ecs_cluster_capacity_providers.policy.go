package policy

// awsECSClusterCapacityProvidersPolicy curates Layer 2 for
// `aws_ecs_cluster_capacity_providers`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// This resource binds capacity providers (FARGATE / FARGATE_SPOT / an
// EC2 ASG provider) to an ECS cluster and pins the default-strategy
// weights/base used when a new service doesn't supply its own strategy.
// Identity is (cluster_name, id). Out-of-band edits silently retarget
// where new tasks land — high-signal drift.
//
// Drift bundle 10 (#482): scalars use DriftSemanticExact;
// `capacity_providers` is a list marked DriftSemanticWholeList so
// add/remove of providers flags as drift. The nested
// `default_capacity_provider_strategy[]` block is left uncurated —
// block-level drift is a follow-up. No tag surface.
var awsECSClusterCapacityProvidersPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent cluster -----------------------------------------
	"cluster_name": {
		// Pointer to the parent aws_ecs_cluster. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — provider set --------------------------------------------
	"capacity_providers": {
		// FARGATE / FARGATE_SPOT / an EC2 ASG provider name. Adding /
		// removing changes the eligible scheduling targets.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
}

func init() {
	Register("aws_ecs_cluster_capacity_providers", awsECSClusterCapacityProvidersPolicy)
}
