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
var awsSQSQueuePolicy = Map{
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_master_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"redrive_policy.deadLetterTargetArn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"redrive_policy.maxReceiveCount": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"sqs_managed_sse_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"visibility_timeout_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"delay_seconds": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"receive_wait_time_seconds": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"message_retention_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"max_message_size": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"fifo_queue": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"content_based_deduplication": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
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
