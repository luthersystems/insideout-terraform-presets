package policy

var googleComputeGlobalForwardingRulePolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Wiring — target proxy + VPC.
	"target": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},
	"subnetwork": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"ip_address": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"ip_protocol": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"ip_version": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"port_range": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
		ChangeRisk: ChangeMayReplace,
	},
	"load_balancing_scheme": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_global_forwarding_rule", googleComputeGlobalForwardingRulePolicy)
}
