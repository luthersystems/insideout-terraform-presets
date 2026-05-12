package policy

var googleComputeForwardingRulePolicy = Map{
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

	// Wiring — target backend / proxy / service, plus VPC association.
	"target": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"backend_service": {
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
	"ports": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
		ChangeRisk: ChangeMayReplace,
	},
	"all_ports": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"load_balancing_scheme": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"network_tier": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	"allow_global_access": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"allow_psc_global_access": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"service_label": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_forwarding_rule", googleComputeForwardingRulePolicy)
}
