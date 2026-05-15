package policy

// awsAppautoscalingPolicyPolicy curates Layer 2 for `aws_appautoscaling_policy`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An App Auto Scaling Policy binds a scaling rule (target-tracking or
// step-scaling) to a scalable target. Identity is
// (name, service_namespace, resource_id, scalable_dimension); changing
// any forces replacement. policy_type picks target-tracking vs
// step-scaling and gates which nested block is meaningful.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact; the
// computed `alarm_arns` list (CW alarms App Auto Scaling created) is
// compared WholeList — out-of-band changes to that set indicate an
// invariant break.
var awsAppautoscalingPolicyPolicy = Map{
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
	"service_namespace": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"scalable_dimension": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"policy_type": {
		// "TargetTrackingScaling" | "StepScaling". Picks which nested
		// configuration block is meaningful.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — target reference + alarms list -------------------------
	"resource_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"alarm_arns": {
		// CloudWatch alarms that App Auto Scaling created on behalf of
		// this policy. Whole-list compare: an unexpected change in the
		// alarm set is one diff entry.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Target-tracking knobs -------------------------------------------
	"target_tracking_scaling_policy_configuration.target_value": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"target_tracking_scaling_policy_configuration.disable_scale_in": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"target_tracking_scaling_policy_configuration.scale_in_cooldown": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"target_tracking_scaling_policy_configuration.scale_out_cooldown": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"target_tracking_scaling_policy_configuration.predefined_metric_specification.predefined_metric_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"target_tracking_scaling_policy_configuration.predefined_metric_specification.resource_label": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Step-scaling knobs -----------------------------------------------
	"step_scaling_policy_configuration.adjustment_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"step_scaling_policy_configuration.cooldown": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"step_scaling_policy_configuration.metric_aggregation_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"step_scaling_policy_configuration.min_adjustment_magnitude": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_appautoscaling_policy", awsAppautoscalingPolicyPolicy)
}
