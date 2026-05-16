package policy

// awsEcsClusterPolicy curates Layer 2 for `aws_ecs_cluster`.
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// Cluster shape is small: `name` is identity, `setting` carries the
// `containerInsights` toggle, `configuration` wires the
// execute-command logging path, and `service_connect_defaults` points
// at the Cloud Map namespace for service discovery.
//
// Drift bundle (#482): scalar attributes use DriftSemanticExact. Tags
// use tagPolicy() with DriftSemanticNone.
var awsEcsClusterPolicy = Map{
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

	// Cluster setting (containerInsights, etc.) ------------------------
	"setting.name": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"setting.value": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Execute-command logging ------------------------------------------
	"configuration.execute_command_configuration.logging": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"configuration.execute_command_configuration.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"configuration.execute_command_configuration.log_configuration.cloud_watch_encryption_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"configuration.execute_command_configuration.log_configuration.cloud_watch_log_group_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Managed-storage encryption ---------------------------------------
	"configuration.managed_storage_configuration.fargate_ephemeral_storage_kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"configuration.managed_storage_configuration.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Service Connect defaults -----------------------------------------
	"service_connect_defaults.namespace": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_ecs_cluster", awsEcsClusterPolicy)
}
