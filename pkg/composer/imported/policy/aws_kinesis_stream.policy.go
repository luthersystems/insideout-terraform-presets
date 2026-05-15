package policy

// awsKinesisStreamPolicy curates Layer 2 for `aws_kinesis_stream`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Kinesis Data Stream is a sharded ordered append-only log. Identity
// is (name, arn). The (shard_count + retention_period + stream_mode)
// triple is the operational sizing — drift on any is a performance
// regression. encryption_type+kms_key_id govern at-rest encryption.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact;
// `shard_level_metrics` is a list-shaped attribute (the set of metrics
// the stream emits) compared WholeList. Tags use tagPolicy().
var awsKinesisStreamPolicy = Map{
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

	// Wiring — KMS key for at-rest encryption -------------------------
	"kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — security + sizing --------------------------------------
	"encryption_type": {
		// NONE | KMS — flipping is a compliance event.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"shard_count": {
		// Only meaningful with stream_mode=PROVISIONED. Edits implicitly
		// resharded — propose via approval.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"retention_period": {
		// 24-8760h. Cost + reliability tradeoff.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"enforce_consumer_deletion": {
		// Destructive flag — system-owned.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditSystemOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Stream-mode block (PROVISIONED vs ON_DEMAND) --------------------
	"stream_mode_details.stream_mode": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Enhanced-monitoring metric set (list-shaped) --------------------
	"shard_level_metrics": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_kinesis_stream", awsKinesisStreamPolicy)
}
