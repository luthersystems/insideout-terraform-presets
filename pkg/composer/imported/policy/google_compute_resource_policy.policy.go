package policy

// googleComputeResourcePolicyPolicy curates Layer 2 for
// `google_compute_resource_policy`. All curated leaves are scalar
// (name, region, description), so DriftSemanticExact is the
// meaningful comparison.
var googleComputeResourcePolicyPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_resource_policy", googleComputeResourcePolicyPolicy)
}
