package policy

// awsApprunnerServicePolicy curates Layer 2 for `aws_apprunner_service`.
//
// #623 backfill for the aws/apprunner preset (#598 / #620). An App
// Runner service is a managed-container HTTP workload — analogous to
// gcp/cloud_run_v2_service. The high-value drift surfaces are:
//
//   - Wiring fields (auto_scaling_configuration_arn, the access /
//     instance role ARNs threaded through source_configuration +
//     instance_configuration, the egress vpc_connector_arn, and the KMS
//     encryption_configuration.kms_key). Silent rebinds re-purpose the
//     service's identity surface — DriftSemanticExact + Security pillar.
//   - The deployed image identifier (source_configuration.image_repository.
//     image_identifier) — an out-of-band tag bump is what
//     "production silently runs a different build" looks like.
//   - Public ingress (network_configuration.ingress_configuration.
//     is_publicly_accessible) — flipping this to true exposes a
//     previously-internal service to the public internet.
var awsApprunnerServicePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"service_id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"service_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"service_url": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"status": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — top-level cross-resource refs --------------------------
	"auto_scaling_configuration_arn": {
		Role: RoleWiring, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Encryption (CMK for source artifact bytes + logs) --------------
	"encryption_configuration.kms_key": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Observability binding -----------------------------------------
	"observability_configuration.observability_configuration_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"observability_configuration.observability_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Source configuration — image-based deploy path -----------------
	"source_configuration.auto_deployments_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"source_configuration.authentication_configuration.access_role_arn": {
		// IAM role App Runner's build plane assumes to pull from ECR.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"source_configuration.authentication_configuration.connection_arn": {
		// CodeStar connection ARN for git-based source.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"source_configuration.image_repository.image_identifier": {
		// ECR image URI — what the service actually runs. An out-of-band
		// tag bump silently changes production code.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"source_configuration.image_repository.image_repository_type": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"source_configuration.image_repository.image_configuration.port": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"source_configuration.image_repository.image_configuration.start_command": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"source_configuration.image_repository.image_configuration.runtime_environment_variables": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},
	"source_configuration.image_repository.image_configuration.runtime_environment_secrets": {
		// Secret refs (ARNs / SSM names) — operationally meaningful but
		// not secret values themselves; Redacted handles UI display.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:        EditRequiresApproval,
		Sensitivity: SensitivityRedacted,
	},

	// Instance configuration — CPU / memory / per-task IAM ------------
	"instance_configuration.cpu": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"instance_configuration.memory": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"instance_configuration.instance_role_arn": {
		// IAM role running tasks assume.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Health check ---------------------------------------------------
	"health_check_configuration.protocol": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"health_check_configuration.path": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"health_check_configuration.interval": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"health_check_configuration.timeout": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"health_check_configuration.healthy_threshold": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"health_check_configuration.unhealthy_threshold": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Network configuration -----------------------------------------
	"network_configuration.ip_address_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"network_configuration.ingress_configuration.is_publicly_accessible": {
		// Silent flip to true exposes a previously-internal service.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"network_configuration.egress_configuration.egress_type": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"network_configuration.egress_configuration.vpc_connector_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_apprunner_service", awsApprunnerServicePolicy)
}
