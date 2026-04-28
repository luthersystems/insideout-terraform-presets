package policy

// awsSQSQueuePolicy matches the worked example in
// docs/managed-resource-tiers.md "Layer 2 — hand-curated field policy
// map" verbatim, with arn added as a UI-visible identity attribute and
// the JSON projection rules registered for redrive_policy subpaths.
var awsSQSQueuePolicy = Map{
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"kms_master_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
	},
	"redrive_policy.deadLetterTargetArn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"redrive_policy.maxReceiveCount": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"sqs_managed_sse_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	"visibility_timeout_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"delay_seconds": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"receive_wait_time_seconds": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"message_retention_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"max_message_size": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"fifo_queue": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"content_based_deduplication": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
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
