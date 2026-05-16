package policy

// awsCloudwatchEventRulePolicy curates Layer 2 for
// `aws_cloudwatch_event_rule` (EventBridge rule). Cloud-control-routed
// enrichment already produces typed Attrs; this map adds the curated
// surface to flip the type from Enrichable to DriftDetectable.
//
// Event rules route matched events from an event bus to one or more
// targets. The identity is (`event_bus_name`, `name`). The two
// behavior modes are mutually exclusive on the API side but the
// schema exposes both: pattern-based routing (`event_pattern`, a JSON
// document) or cron-based dispatch (`schedule_expression`).
//
// Drift bundle 3 (#482): scalar attributes use DriftSemanticExact.
// `event_pattern` is JSON; we diff it Exact at the curated layer and
// leave canonical-form normalization to the diff projection layer.
var awsCloudwatchEventRulePolicy = Map{
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
	"name_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"event_bus_name": {
		// Pinned at create — rules live on a specific bus.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — IAM target invocation role -----------------------------
	"role_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — behavior knobs -----------------------------------------
	"event_pattern": {
		// JSON pattern document. Diff'd Exact at the curated layer.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"schedule_expression": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"state": {
		// ENABLED | DISABLED | ENABLED_WITH_ALL_CLOUDTRAIL_MANAGEMENT_EVENTS
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"is_enabled": {
		// Deprecated alias for `state` — surface drift in case state was
		// edited out-of-band via the legacy path.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"force_destroy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_cloudwatch_event_rule", awsCloudwatchEventRulePolicy)
}
