package policy

var googleComputeSecurityPolicyPolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning — top-level policy knobs.
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"type": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning — rule block (priority + action + match selectors).
	"rule.priority": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"rule.action": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"rule.description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"rule.preview": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"rule.match.versioned_expr": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"rule.match.config.src_ip_ranges": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"rule.match.expr.expression": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// Adaptive protection (DDoS adaptive defense).
	"adaptive_protection_config.layer_7_ddos_defense_config.enable": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"adaptive_protection_config.layer_7_ddos_defense_config.rule_visibility": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Advanced options (JSON parsing, request body inspection).
	"advanced_options_config.json_parsing": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"advanced_options_config.log_level": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_security_policy", googleComputeSecurityPolicyPolicy)
}
