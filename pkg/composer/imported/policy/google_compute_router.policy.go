package policy

var googleComputeRouterPolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Wiring — VPC.
	"network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"encrypted_interconnect_router": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	// BGP block — ASN is identity-shaped (replace on change).
	"bgp.asn": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
	},
	"bgp.advertise_mode": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"bgp.advertised_groups": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"bgp.keepalive_interval": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_router", googleComputeRouterPolicy)
}
