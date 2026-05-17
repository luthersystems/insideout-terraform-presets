package policy

// awsBedrockModelInvocationLoggingConfigurationPolicy curates Layer 2 for
// `aws_bedrock_model_invocation_logging_configuration`.
//
// Bundle E (#482): hand-rolled Bucket-C coverage push lifts AWS to 98%
// Enrichable. This resource is a per-region account singleton — its
// import id is the region itself, and there is no user-supplied name.
// The TF surface enumerates a logging_config block with three boolean
// data-class toggles (text / image / embedding) and two nested
// destination blocks (CloudWatch log group + S3 bucket).
//
// All curated leaves are scalar — DriftSemanticExact is the meaningful
// comparison. Destination wiring (log group name, S3 bucket name, IAM
// role ARN) is RoleWiring so the composer's graph resolver owns the
// edits; the boolean data-class toggles are RoleTuning under the
// Security pillar.
var awsBedrockModelInvocationLoggingConfigurationPolicy = Map{
	// Identity ---------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — data-class toggles --------------------------------------
	"logging_config.text_data_delivery_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.image_data_delivery_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.embedding_data_delivery_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — CloudWatch destination ----------------------------------
	"logging_config.cloudwatch_config.log_group_name": {
		// Log group reference — the composer's graph resolver edits the
		// underlying aws_cloudwatch_log_group resource.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.cloudwatch_config.role_arn": {
		// IAM role wiring — same Wiring discipline as KMS keys.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.cloudwatch_config.large_data_delivery_s3_config.bucket_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.cloudwatch_config.large_data_delivery_s3_config.key_prefix": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — S3 destination ------------------------------------------
	"logging_config.s3_config.bucket_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging_config.s3_config.key_prefix": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_bedrock_model_invocation_logging_configuration", awsBedrockModelInvocationLoggingConfigurationPolicy)
}
