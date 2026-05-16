package policy

// googleProjectServicePolicy curates Layer 2 for `google_project_service`.
//
// google_project_service represents the enabled-state toggle for a
// specific Google API on a project (e.g. secretmanager.googleapis.com).
// The resource has a trivial schema — `project` + `service` form the
// identity, and a pair of disable_* flags govern the destroy contract.
// All non-identity fields are user tuning knobs the model can safely
// reason about; nothing here is sensitive.
var googleProjectServicePolicy = Map{
	// Identity
	"id": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"service": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — destroy-time toggles. Pure lifecycle controls; safe to
	// edit and have no impact on the live resource until destroy.
	"disable_dependent_services": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"disable_on_destroy": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_project_service", googleProjectServicePolicy)
}
