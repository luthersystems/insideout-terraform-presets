package policy

// awsLambdaEventSourceMappingPolicy curates Layer 2 for
// `aws_lambda_event_source_mapping`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// An event-source mapping (ESM) wires a poll-based source (Kinesis /
// DynamoDB Streams / SQS / Amazon MQ / Kafka / MSK / DocumentDB) into
// a Lambda function. Identity is (uuid, function_arn); the wiring axes
// are (event_source_arn, function_name) plus the batching / starting-
// position knobs that drive operational behavior. Drift on `enabled`
// is high-signal — silently disabling an ESM stalls the pipeline.
//
// Drift bundle 9 (#482): scalars use DriftSemanticExact; list-shaped
// attributes (function_response_types, queues, topics) compare WholeList.
//
// Depth-pass extras (#482 follow-up): adds the nested-block
// `destination_config.on_failure.destination_arn` (DLQ wiring),
// `filter_criteria.filter.pattern` (per-event filter JSON),
// `scaling_config.maximum_concurrency`, `amazon_managed_kafka_event_source_config.consumer_group_id`,
// `self_managed_kafka_event_source_config.consumer_group_id`,
// `document_db_event_source_config.*` (database / collection /
// full_document), and the
// `self_managed_event_source.endpoints` + `source_access_configuration.*`
// authentication tuples for SMK / MSK / MQ sources.
var awsLambdaEventSourceMappingPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"uuid": {
		// The AWS-assigned identifier for the ESM.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"function_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"last_modified": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"last_processing_result": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"state": {
		// Creating | Enabled | Disabled | Updating | Deleting
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"state_transition_reason": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — event source + target function -------------------------
	"event_source_arn": {
		// Pointer to the stream/queue/cluster. Pinned at create for
		// most source types; cross-resource identity.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"function_name": {
		// Name or ARN of the Lambda function. Identity-leaf per lint
		// (`function_name` is a well-known identity attribute); the
		// target-function ARN is the wiring axis but identity rules
		// pin Edit=Never. Cross-resource pin = ChangeAlwaysReplace.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — operational knobs --------------------------------------
	"enabled": {
		// Disabling silently stalls the pipeline — high-signal drift.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"batch_size": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"maximum_batching_window_in_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"maximum_record_age_in_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"maximum_retry_attempts": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"bisect_batch_on_function_error": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"parallelization_factor": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"tumbling_window_in_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"starting_position": {
		// LATEST | TRIM_HORIZON | AT_TIMESTAMP. Pinned at create for
		// Kinesis / DynamoDB.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"starting_position_timestamp": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_key_arn": {
		// Customer-managed CMK for filter-criteria encryption.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — list-shaped source bindings (Kafka topics / SQS queues) -
	"queues": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"topics": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"function_response_types": {
		// ReportBatchItemFailures opt-in.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tags --------------------------------------------------------------
	// DLQ + filter + scaling -------------------------------------------
	"destination_config.on_failure.destination_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"filter_criteria.filter.pattern": {
		// Per-event JSON filter expression — drift here silently widens
		// or narrows which messages reach the function.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"scaling_config.maximum_concurrency": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Kafka consumer groups --------------------------------------------
	"amazon_managed_kafka_event_source_config.consumer_group_id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"self_managed_kafka_event_source_config.consumer_group_id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// DocumentDB source ------------------------------------------------
	"document_db_event_source_config.database_name": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"document_db_event_source_config.collection_name": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"document_db_event_source_config.full_document": {
		// Default | UpdateLookup.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Self-managed source endpoints + auth -----------------------------
	"self_managed_event_source.endpoints": {
		// Map of broker endpoints (e.g. KAFKA_BOOTSTRAP_SERVERS -> csv).
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly,
	},
	"source_access_configuration.type": {
		// BASIC_AUTH | VPC_SUBNET | VPC_SECURITY_GROUP | SASL_SCRAM_512_AUTH ...
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"source_access_configuration.uri": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_lambda_event_source_mapping", awsLambdaEventSourceMappingPolicy)
}
