package policy

// awsSNSTopicSubscriptionPolicy curates Layer 2 for
// `aws_sns_topic_subscription`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// An SNS topic subscription is the fanout endpoint registration that
// routes published messages from a topic to a delivery target (SQS,
// Lambda, HTTPS, email, SMS, Kinesis Firehose, application). Identity
// is (arn, id). (topic_arn, protocol, endpoint) is the wiring tuple.
// filter_policy + filter_policy_scope drive per-subscription delivery
// filtering — drift on them silently re-shapes which messages fan out.
// redrive_policy points the failure path at a DLQ.
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact. No tag
// surface — SNS subscriptions are not directly tagged.
var awsSNSTopicSubscriptionPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"owner_id": {
		// AWS account that owns the subscription; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — topic + delivery target --------------------------------
	"topic_arn": {
		// Pointer to the parent aws_sns_topic. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"protocol": {
		// sqs / lambda / https / email / sms / firehose / application;
		// pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"endpoint": {
		// Delivery target (queue ARN, function ARN, URL, email, etc.).
		// Pinned at create — retargeting is a replace.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"subscription_role_arn": {
		// IAM role SNS assumes to deliver to firehose targets.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — delivery filtering + retries ---------------------------
	"filter_policy": {
		// JSON filter applied per-subscription. Drift silently re-shapes
		// the message fanout.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"filter_policy_scope": {
		// MessageAttributes / MessageBody; controls what filter_policy
		// matches against.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"redrive_policy": {
		// DLQ target for failed deliveries (JSON {deadLetterTargetArn}).
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"raw_message_delivery": {
		// bool — strips SNS envelope when true.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_sns_topic_subscription", awsSNSTopicSubscriptionPolicy)
}
