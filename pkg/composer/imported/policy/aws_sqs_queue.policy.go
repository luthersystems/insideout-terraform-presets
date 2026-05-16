package policy

// awsSQSQueuePolicy matches the worked example in
// docs/managed-resource-tiers.md "Layer 2 — hand-curated field policy
// map" verbatim, with arn added as a UI-visible identity attribute and
// the JSON projection rules registered for redrive_policy subpaths.
//
// Bundle D2 (#491): DriftSemantic axis is curated on every non-tag
// entry. All curated leaves are scalar — DriftSemanticExact is the
// meaningful comparison for each.
//
// Note: the redrive_policy.* paths declare the intended drift semantic
// but will not surface a signal through the current pkg/drift/imported
// comparator because redrive_policy is a JSON-encoded string in state
// (see projection.go limitation note — the comparator's resolvePath
// walks map nodes, not JSON-encoded string parents). The DriftSemantic
// declaration is intentionally still curated here so when the
// comparator gains JSON-projection traversal (parallel to the
// projection.go follow-up), these paths light up without a re-curation
// sweep. Tag bags stay DriftSemanticNone (tagPolicy() zero value).
//
// Depth-pass extras (#482 follow-up): adds `id`, `url`, `policy`,
// `name_prefix`, the FIFO-specific knobs (`deduplication_scope`,
// `fifo_throughput_limit`), KMS data-key reuse (`kms_data_key_reuse_period_seconds`),
// and the redrive-allow JSON family. `policy` is the access-policy
// JSON — security-critical, RequiresApproval. The two redrive_policy
// JSON parents (`redrive_policy`, `redrive_allow_policy`) are mapped at
// the parent path with DriftSemanticNone (they're JSON-encoded strings
// the comparator can't decode today — the leaf .deadLetterTargetArn /
// .maxReceiveCount entries above carry the actual semantic).
var awsSQSQueuePolicy = Map{
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"url": {
		// Server-assigned queue URL — same identity tier as arn.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"name_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"policy": {
		// Access policy JSON — security-critical drift surface.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_data_key_reuse_period_seconds": {
		// How long SQS reuses an encryption data key (60-86400s).
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"deduplication_scope": {
		// FIFO-only: "messageGroup" vs "queue" dedupe scope.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"fifo_throughput_limit": {
		// FIFO-only: "perQueue" vs "perMessageGroupId" throughput cap.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"redrive_policy": {
		// JSON-encoded parent — the deadLetterTargetArn / maxReceiveCount
		// leaves above carry the semantic. Parent path itself stays
		// DriftSemanticNone until the comparator gains JSON traversal.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly,
	},
	"redrive_allow_policy": {
		// JSON document scoping who's allowed to redrive INTO this DLQ.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly,
	},
	"kms_master_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"redrive_policy.deadLetterTargetArn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"redrive_policy.maxReceiveCount": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"sqs_managed_sse_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"visibility_timeout_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"delay_seconds": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"receive_wait_time_seconds": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"message_retention_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"max_message_size": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"fifo_queue": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"content_based_deduplication": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	// Tags — adopt awsTagDriftPolicy() (#568): user-set tag drift
	// surfaces as per-key `tags.<key>` mismatches; AWS-managed
	// prefixes are filtered. SQS queues are low-churn messaging
	// infra where the canonical `Project` tag drives InsideOut
	// inspector attribution — stripping it must surface as drift.
	"tags":     awsTagDriftPolicy(),
	"tags_all": awsTagDriftPolicy(),
}

func init() {
	RegisterJSONProjection("aws_sqs_queue", JSONProjection{
		Parent: "redrive_policy", Subpath: "deadLetterTargetArn",
	})
	RegisterJSONProjection("aws_sqs_queue", JSONProjection{
		Parent: "redrive_policy", Subpath: "maxReceiveCount",
	})
	Register("aws_sqs_queue", awsSQSQueuePolicy)
}
