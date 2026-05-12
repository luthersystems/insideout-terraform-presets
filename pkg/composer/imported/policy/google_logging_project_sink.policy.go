package policy

var googleLoggingProjectSinkPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Wiring — destination is a managed resource reference
	// (storage.googleapis.com/buckets/<b>, bigquery.googleapis.com/...,
	// pubsub.googleapis.com/topics/<t>, etc.).
	"destination": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},

	// Tuning — filter expression + enabled state.
	"filter": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"disabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},

	// Writer identity controls whether the sink gets a unique service
	// account (common for cross-project sinks).
	"unique_writer_identity": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
	},
	"custom_writer_identity": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	// writer_identity is the computed SA email — read-only.
	"writer_identity": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},
	// NB: google_logging_project_sink has no `timeouts` block per the
	// provider schema — sink ops are synchronous from the Cloud Logging
	// API's perspective.
}

func init() {
	Register("google_logging_project_sink", googleLoggingProjectSinkPolicy)
}
