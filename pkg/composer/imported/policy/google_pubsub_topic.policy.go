package policy

// googlePubsubTopicPolicy curates Layer 2 for `google_pubsub_topic`.
//
// Bundle D1 (#491): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Scalars use DriftSemanticExact. The single
// list-valued tuning leaf, `message_storage_policy.allowed_persistence_regions`,
// uses DriftSemanticWholeList — the provider returns the residency
// regions in an authored order and a whole-list diff is the meaningful
// signal.
//
// Reliable #1479 follow-up: `labels` adopts gcpLabelDriftPolicy() so
// user-set labels surface as drift (per-key `labels.<keyname>`
// mismatches) while goog-* / insideout-import* control-plane and
// provenance labels are filtered out — matches the legacy reliable
// comparator (compareGooglePubsubTopicAttrs.diffUserLabels) so the
// Surface B per-type-comparator deletion preserves the user-facing
// drift signal.
var googlePubsubTopicPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — encryption + ingestion data sources
	"kms_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.aws_role_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.consumer_arn": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.gcp_service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.aws_kinesis.stream_arn": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.bucket": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"schema_settings.schema": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"message_retention_duration": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"message_storage_policy.allowed_persistence_regions": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"schema_settings.encoding": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.match_glob": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.minimum_object_create_time": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.cloud_storage.text_format.delimiter": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ingestion_data_source_settings.platform_logs_settings.severity": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels — `labels` carries user-set drift signal; computed
	// echoes (`effective_labels`, `terraform_labels`) stay system-only.
	"labels":           gcpLabelDriftPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_pubsub_topic", googlePubsubTopicPolicy)
}
