package policy

// googleComputeNetworkPolicy curates Layer 2 for `google_compute_network`.
//
// Bundle D2 (#491): DriftSemantic axis is curated on every non-timeouts
// entry. All curated leaves are scalar (network identity, routing knobs,
// MTU, boolean flags, description) — DriftSemanticExact is the
// meaningful comparison for each. `google_compute_network` has no
// labels and no list-valued curated fields, so neither WholeList nor
// LabelFilter applies. The `timeouts` singleton is system-owned
// operational metadata and stays DriftSemanticNone via timeoutsPolicy().
var googleComputeNetworkPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"numeric_id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"gateway_ipv4": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — networking semantics
	"auto_create_subnetworks": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"routing_mode": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"mtu": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"delete_default_routes_on_create": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_ula_internal_ipv6": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"network_firewall_policy_enforcement_order": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// google_compute_network has no labels and no timeouts on the
	// surface struct — the timeouts singleton is intentionally
	// included as system-owned operational metadata.
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_network", googleComputeNetworkPolicy)
}
