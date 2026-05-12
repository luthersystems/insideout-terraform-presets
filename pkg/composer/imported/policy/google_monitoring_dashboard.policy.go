package policy

var googleMonitoringDashboardPolicy = Map{
	// Identity (dashboards are named via dashboard_json's displayName; the
	// top-level `name` is the GCP resource name set by the provider on create.)
	"id": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning — entire payload is the JSON document. Treated as a single
	// authored blob; Riley can edit via chat-safe JSON edits but the
	// composer / wizard owns large refactors.
	"dashboard_json": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_monitoring_dashboard", googleMonitoringDashboardPolicy)
}
