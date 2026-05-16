package policy

// googleLoggingProjectSinkPolicy curates Layer 2 for
// `google_logging_project_sink`. Identity scalars + the `destination`
// wiring leaf are tagged DriftSemanticExact so re-pointing of the sink
// destination surfaces. The sink has no list-valued curated fields
// (filter is a single CEL expression); WholeList does not apply.
var googleLoggingProjectSinkPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — destination is a managed resource reference
	// (storage.googleapis.com/buckets/<b>, bigquery.googleapis.com/...,
	// pubsub.googleapis.com/topics/<t>, etc.).
	"destination": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — filter expression + enabled state.
	"filter": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"disabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Writer identity controls whether the sink gets a unique service
	// account (common for cross-project sinks).
	"unique_writer_identity": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"custom_writer_identity": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	// writer_identity is the computed SA email — read-only.
	"writer_identity": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	// NB: google_logging_project_sink has no `timeouts` block per the
	// provider schema — sink ops are synchronous from the Cloud Logging
	// API's perspective.
}

func init() {
	Register("google_logging_project_sink", googleLoggingProjectSinkPolicy)
}
