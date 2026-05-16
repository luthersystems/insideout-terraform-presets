package policy

// googleSecretManagerSecretVersionPolicy curates Layer 2 for
// `google_secret_manager_secret_version`.
//
// Companion to googleSecretManagerSecretPolicy: the parent secret
// holds metadata (identity, replication wiring, rotation schedule);
// this version resource holds the per-version lifecycle state
// (enabled flag, destroy schedule) plus the Sensitive payload bag
// (`secret_data`, `is_secret_data_base64`).
//
// The Sensitive payload is not Curated here — the enricher never
// populates it (Versions.Get does not return payload material), and
// the carrier escalates it via lifecycle.ignore_changes in
// genconfig.cleanup. Drift on `secret_data` would be a false positive
// every refresh, so it stays DriftSemanticNone (the bag default).
var googleSecretManagerSecretVersionPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent secret. Replacement on edit (the version belongs
	// to exactly one secret; rewiring is a recreate).
	"secret": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — per-version lifecycle knobs the enricher reads back.
	"enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"deletion_policy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Sensitive payload — see header. Untracked: Get doesn't return
	// payload, drift comparison would always fire, and exposing the
	// material would defeat the carrier's ignore_changes escalation.
	"secret_data":           tagPolicy(),
	"is_secret_data_base64": tagPolicy(),

	// Observability-only timestamps. RoleIdentity because the engine's
	// axis model doesn't have a separate Observability role (#491);
	// EditNever + VisibilitySummaryVisible captures the intent without
	// triggering drift comparisons.
	"create_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
	},
	"destroy_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_secret_manager_secret_version", googleSecretManagerSecretVersionPolicy)
}
