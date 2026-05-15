package policy

// googleCloudfunctions2FunctionPolicy curates Layer 2 for
// `google_cloudfunctions2_function`. Identity scalars and the KMS
// wiring leaf are tagged DriftSemanticExact so drift detection
// surfaces ownership / encryption deviation; other curated fields
// stay DriftSemanticNone until per-leaf comparators land. The type
// surface has no curated list leaves.
var googleCloudfunctions2FunctionPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — KMS key reference.
	"kms_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — function-level metadata.
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"environment": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Build config — runtime + entrypoint + source.
	"build_config.runtime": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"build_config.entry_point": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"build_config.docker_repository": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"build_config.service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"build_config.source.storage_source.bucket": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"build_config.source.storage_source.object": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
	},
	"build_config.environment_variables": tagPolicy(),

	// Service config — runtime scaling + invoker.
	"service_config.service_account_email": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"service_config.available_memory": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"service_config.available_cpu": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"service_config.timeout_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"service_config.max_instance_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"service_config.min_instance_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"service_config.max_instance_request_concurrency": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"service_config.ingress_settings": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
	},
	"service_config.all_traffic_on_latest_revision": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"service_config.vpc_connector": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"service_config.vpc_connector_egress_settings": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"service_config.environment_variables": tagPolicy(),

	// Event trigger.
	"event_trigger.trigger_region": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"event_trigger.event_type": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
	},
	"event_trigger.pubsub_topic": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"event_trigger.service_account_email": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"event_trigger.retry_policy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_cloudfunctions2_function", googleCloudfunctions2FunctionPolicy)
}
