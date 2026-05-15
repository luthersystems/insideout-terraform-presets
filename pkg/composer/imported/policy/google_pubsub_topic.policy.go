package policy

// googlePubsubTopicPolicy curates Layer 2 for `google_pubsub_topic`.
//
// Bundle D1 (#491): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Scalars use DriftSemanticExact. The single
// list-valued tuning leaf, `message_storage_policy.allowed_persistence_regions`,
// uses DriftSemanticWholeList — the provider returns the residency
// regions in an authored order and a whole-list diff is the meaningful
// signal. Labels stay at the tagPolicy() default (DriftSemanticNone);
// LabelFilter coverage on user-author labels is deferred until the
// drift comparator's redacted-mode output is in place (axes.go
// follow-up referenced in aws_lambda_function).
var googlePubsubTopicPolicy = Map{
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

	// Wiring — encryption + ingestion data sources
	"kms_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.aws_role_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.consumer_arn": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.gcp_service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.stream_arn": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.bucket": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"schema_settings.schema": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"message_retention_duration": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"message_storage_policy.allowed_persistence_regions": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"schema_settings.encoding": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.match_glob": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.minimum_object_create_time": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.text_format.delimiter": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.platform_logs_settings.severity": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels — system-owned. DriftSemantic deferred (see file comment).
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_pubsub_topic", googlePubsubTopicPolicy)
}
