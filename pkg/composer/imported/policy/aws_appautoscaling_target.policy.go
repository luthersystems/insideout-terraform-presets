package policy

// awsAppautoscalingTargetPolicy curates Layer 2 for `aws_appautoscaling_target`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An App Auto Scaling Target registers a scalable dimension on a resource
// (ECS service tasks, DynamoDB throughput, Aurora read replicas, etc.).
// Identity is the (service_namespace, resource_id, scalable_dimension)
// triple — all three are AlwaysReplace and define which entity is being
// scaled. The min/max capacity range is the active tuning knob.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact;
// suspended_state surfaces nested booleans that callers can flip
// independently. Tags use tagPolicy().
var awsAppautoscalingTargetPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"service_namespace": {
		// Which service the scalable dimension lives under (ecs, dynamodb, ...).
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"scalable_dimension": {
		// e.g. ecs:service:DesiredCount. Identifies which dimension on the
		// target resource is being scaled.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"resource_id": {
		// Pointer to the target resource (e.g. service/cluster/svc-name).
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — role + suspended-state map ------------------------------
	"role_arn": {
		// Service-linked role ARN used by App Auto Scaling.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — capacity range -----------------------------------------
	"min_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"max_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Suspended-state controls — each axis can be paused independently.
	"suspended_state.dynamic_scaling_in_suspended": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"suspended_state.dynamic_scaling_out_suspended": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"suspended_state.scheduled_scaling_suspended": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_appautoscaling_target", awsAppautoscalingTargetPolicy)
}
