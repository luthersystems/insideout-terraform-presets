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
//
// Depth-pass extras (#482 follow-up): adds the deployment-package hash
// scalars (`code_sha256`, `source_code_hash`, `source_code_size`,
// `last_modified`), the container-image variant (`image_uri`,
// `filename`), code-signing identity (`signing_job_arn`,
// `signing_profile_version_arn`), the `description` text knob, `id`,
// and the two destroy-behavior switches (`skip_destroy`,
// `replace_security_groups_on_destroy`).
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
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"qualified_arn": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"invoke_arn": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"qualified_invoke_arn": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Deployment-package hash / size telemetry --------------------------
	"code_sha256": {
		// Provider-reported hash of the deployed package. Drift here
		// indicates an out-of-band redeploy.
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"source_code_hash": {
		// Author-side hash input that gates re-publish. Mismatch =
		// caller bumped source without redeploy (or vice versa).
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"source_code_size": {
		// Provider-reported package size in bytes.
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"last_modified": {
		// Provider timestamp of last update — drift on this without a
		// matching Terraform change implies out-of-band deploy.
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Container-image / file deployment surface -------------------------
	"image_uri": {
		// For package_type=Image: the ECR image URI.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"filename": {
		// Zip path for the deployment package (local-file deploys).
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Code-signing identity --------------------------------------------
	"signing_job_arn": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"signing_profile_version_arn": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — required role and supporting cross-references
	"role": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"code_signing_config_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"dead_letter_config.target_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"file_system_config.arn": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"file_system_config.local_mount_path": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.log_group": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc_config.security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_config.subnet_ids": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_config.vpc_id": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"layers": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"replacement_security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"s3_bucket": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"s3_key": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"s3_object_version": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — runtime configuration
	"runtime": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"handler": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"memory_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"timeout": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"architectures": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"package_type": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"reserved_concurrent_executions": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"publish": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"skip_destroy": {
		// Whether to leave the resource in AWS on destroy.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"replace_security_groups_on_destroy": {
		// Whether to swap to placeholder SGs before delete (ENI cleanup).
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — nested blocks
	"ephemeral_storage.size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"tracing_config.mode": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"snap_start.apply_on": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"image_config.command": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"image_config.entry_point": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"image_config.working_directory": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.application_log_level": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.system_log_level": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.log_format": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
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
