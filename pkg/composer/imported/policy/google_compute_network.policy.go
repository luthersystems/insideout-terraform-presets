package policy

var googleComputeNetworkPolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"numeric_id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"gateway_ipv4": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},

	// Tuning — networking semantics
	"auto_create_subnetworks": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	"routing_mode": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"mtu": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"delete_default_routes_on_create": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"enable_ula_internal_ipv6": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"network_firewall_policy_enforcement_order": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// google_compute_network has no labels and no timeouts on the
	// surface struct — the timeouts singleton is intentionally
	// included as system-owned operational metadata.
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_network", googleComputeNetworkPolicy)
}
