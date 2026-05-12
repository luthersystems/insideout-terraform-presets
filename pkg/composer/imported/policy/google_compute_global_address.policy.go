package policy

var googleComputeGlobalAddressPolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Wiring — VPC association for INTERNAL purpose.
	"network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning
	"address": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"address_type": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"purpose": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"ip_version": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"prefix_length": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_global_address", googleComputeGlobalAddressPolicy)
}
