package policy

var awsLambdaFunctionPolicy = Map{
	// Identity
	"arn":                  {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"function_name":        {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"version":              {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"qualified_arn":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"invoke_arn":           {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"qualified_invoke_arn": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},

	// Wiring — required role and supporting cross-references
	"role": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"code_signing_config_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"dead_letter_config.target_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"file_system_config.arn": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"file_system_config.local_mount_path": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"logging_config.log_group": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"vpc_config.security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"vpc_config.subnet_ids": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"vpc_config.vpc_id": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"layers": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"replacement_security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"s3_bucket": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"s3_key": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"s3_object_version": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Tuning — runtime configuration
	"runtime": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"handler": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"memory_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"timeout": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"architectures": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		ChangeRisk: ChangeMayReplace,
	},
	"package_type": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"reserved_concurrent_executions": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"publish": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Tuning — nested blocks
	"ephemeral_storage.size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"tracing_config.mode": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"snap_start.apply_on": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"image_config.command": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"image_config.entry_point": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"image_config.working_directory": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"logging_config.application_log_level": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"logging_config.system_log_level": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"logging_config.log_format": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Sensitive: environment variables routinely contain credentials.
	// Hidden from Riley; only system code reads/writes them.
	"environment.variables": {
		Role: RoleTuning, Pillar: PillarSecurity,
		Visibility: VisibilityHidden, Edit: EditSystemOnly,
		Sensitivity: SensitivitySensitive,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_lambda_function", awsLambdaFunctionPolicy)
}
