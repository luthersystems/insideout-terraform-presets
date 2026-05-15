package policy

// awsLambdaFunctionPolicy curates Layer 2 for `aws_lambda_function`.
//
// Bundle D1 (#491): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Scalar attributes (ARNs, runtime/handler, memory
// and timeout knobs) use DriftSemanticExact. List-valued attributes
// (`layers`, `architectures`, `vpc_config.security_group_ids`,
// `vpc_config.subnet_ids`, `replacement_security_group_ids`, the
// image_config command/entry_point) use DriftSemanticWholeList — order
// is provider-visible and the per-element shape is opaque scalars, so
// whole-list compare is the meaningful granularity. The sensitive
// `environment.variables` map stays DriftSemanticNone: per the Layer 2
// contract, raw values for Sensitive fields must never flow through the
// drift output (FieldMismatch.Snapshot / .Cloud would leak the secret).
var awsLambdaFunctionPolicy = Map{
	// Identity
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"function_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"qualified_arn": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"invoke_arn": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"qualified_invoke_arn": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — required role and supporting cross-references
	"role": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"code_signing_config_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"dead_letter_config.target_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"file_system_config.arn": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"file_system_config.local_mount_path": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.log_group": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc_config.security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_config.subnet_ids": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_config.vpc_id": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"layers": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"replacement_security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"s3_bucket": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"s3_key": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"s3_object_version": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — runtime configuration
	"runtime": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"handler": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"memory_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"timeout": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"architectures": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"package_type": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"reserved_concurrent_executions": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"publish": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — nested blocks
	"ephemeral_storage.size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"tracing_config.mode": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"snap_start.apply_on": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"image_config.command": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"image_config.entry_point": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"image_config.working_directory": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.application_log_level": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.system_log_level": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.log_format": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Sensitive: environment variables routinely contain credentials.
	// Hidden from the interactive agent; only system code reads/writes them.
	//
	// DriftSemantic stays None: drift output carries raw Snapshot/Cloud
	// values, and Sensitive values must not flow through that channel.
	// Once the drift surface gains a redaction mode (axes.go follow-up),
	// this can move to a redacted-LabelFilter or similar.
	"environment.variables": {
		Role: RoleTuning, Pillar: PillarSecurity,
		Visibility:  VisibilityHidden,
		Edit:        EditSystemOnly,
		Sensitivity: SensitivitySensitive,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_lambda_function", awsLambdaFunctionPolicy)
}
