package policy

var googlePubsubSubscriptionPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Wiring — required topic plus delivery / DLQ targets
	"topic": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"dead_letter_policy.dead_letter_topic": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"bigquery_config.table": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"bigquery_config.service_account_email": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"cloud_storage_config.bucket": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"cloud_storage_config.service_account_email": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"push_config.oidc_token.service_account_email": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Push config — endpoints and attributes can carry tokens.
	"push_config.push_endpoint": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, Sensitivity: SensitivityRedacted,
	},
	"push_config.attributes": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, Sensitivity: SensitivityRedacted,
	},
	"push_config.oidc_token.audience": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, Sensitivity: SensitivityRedacted,
	},

	// Tuning — delivery semantics
	"ack_deadline_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"enable_exactly_once_delivery": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"enable_message_ordering": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"filter": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"message_retention_duration": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"retain_acked_messages": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"expiration_policy.ttl": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Retry + DLQ knobs
	"retry_policy.minimum_backoff": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"retry_policy.maximum_backoff": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"dead_letter_policy.max_delivery_attempts": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// BigQuery + Cloud Storage flags
	"bigquery_config.drop_unknown_fields": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"bigquery_config.use_table_schema": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"bigquery_config.use_topic_schema": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"bigquery_config.write_metadata": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.filename_prefix": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.filename_suffix": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.filename_datetime_format": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.max_bytes": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.max_duration": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.max_messages": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.avro_config.use_topic_schema": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"cloud_storage_config.avro_config.write_metadata": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Labels — system-owned.
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_pubsub_subscription", googlePubsubSubscriptionPolicy)
}
