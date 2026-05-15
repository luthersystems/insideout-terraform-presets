package policy

// googleMonitoringDashboardPolicy curates Layer 2 for
// `google_monitoring_dashboard`. Identity scalars are DriftSemanticExact.
// `dashboard_json` is the entire authored payload — exact-compare is the
// right comparator (any change in the JSON body is meaningful), even
// though the value is structured; per-leaf comparators inside the JSON
// document are a future-comparator concern, not a WholeList one.
var googleMonitoringDashboardPolicy = Map{
	// Identity. Dashboards have no top-level `name` field in the schema —
	// the dashboard's user-facing label lives inside dashboard_json's
	// displayName property. `id` is the provider-assigned resource ID.
	"id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — entire payload is the JSON document. Treated as a single
	// authored blob; the interactive agent can edit via chat-safe JSON
	// edits but the composer / wizard owns large refactors.
	"dashboard_json": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_monitoring_dashboard", googleMonitoringDashboardPolicy)
}
