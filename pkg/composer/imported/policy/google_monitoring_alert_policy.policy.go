package policy

// googleMonitoringAlertPolicyPolicy curates Layer 2 for
// `google_monitoring_alert_policy`. Identity scalars + the
// `combiner` / `enabled` tuning scalars are DriftSemanticExact;
// `notification_channels` is a list of managed channel references
// where set-membership drift is the actionable signal but per-element
// removal is not independently meaningful — DriftSemanticWholeList.
var googleMonitoringAlertPolicyPolicy = Map{
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
	"display_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — notification channels are managed resources.
	"notification_channels": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning — combiner + enable + severity.
	"combiner": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"severity": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Conditions block — match conditions are core alert semantics.
	"conditions.display_name": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},
	"conditions.condition_threshold.filter": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval,
	},
	"conditions.condition_threshold.threshold_value": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval,
	},
	"conditions.condition_threshold.duration": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},
	"conditions.condition_threshold.comparison": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},

	// Alert strategy — auto-close + notification rate.
	"alert_strategy.auto_close": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},
	"alert_strategy.notification_rate_limit.period": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},

	// Documentation block — content + subject for the alert body.
	"documentation.content": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},
	"documentation.subject": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},
	"documentation.mime_type": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},

	// Labels are exposed as user_labels for monitoring resources.
	"user_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_monitoring_alert_policy", googleMonitoringAlertPolicyPolicy)
}
