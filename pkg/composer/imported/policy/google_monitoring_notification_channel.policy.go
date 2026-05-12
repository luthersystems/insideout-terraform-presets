package policy

var googleMonitoringNotificationChannelPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"display_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning — enable / description.
	"enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"force_delete": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
	},

	// Type-specific config (email address, slack token URL, pubsub topic,
	// pagerduty service key…) lives in `labels` (display) and
	// `sensitive_labels` (secrets). Both are map-shaped per the schema:
	// labels is the type's PUBLIC config payload (e.g. email_address), and
	// sensitive_labels carries provider-side secret refs.
	"labels":           tagPolicy(),
	"sensitive_labels": tagPolicy(),

	// User-applied labels (informational).
	"user_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_monitoring_notification_channel", googleMonitoringNotificationChannelPolicy)
}
