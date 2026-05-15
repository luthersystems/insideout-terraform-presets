package policy

var googleMonitoringDashboardPolicy = Map{
	// Identity. Dashboards have no top-level `name` field in the schema —
	// the dashboard's user-facing label lives inside dashboard_json's
	// displayName property. `id` is the provider-assigned resource ID.
	"id": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning — entire payload is the JSON document. Treated as a single
	// authored blob; the interactive agent can edit via chat-safe JSON
	// edits but the composer / wizard owns large refactors.
	"dashboard_json": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_monitoring_dashboard", googleMonitoringDashboardPolicy)
}
