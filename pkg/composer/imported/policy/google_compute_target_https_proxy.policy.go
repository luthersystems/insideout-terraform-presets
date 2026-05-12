package policy

var googleComputeTargetHttpsProxyPolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Wiring — URL map + certificates + policies.
	"url_map": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"ssl_certificates": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"certificate_manager_certificates": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"certificate_map": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"ssl_policy": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"server_tls_policy": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"quic_override": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"proxy_bind": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"http_keep_alive_timeout_sec": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"tls_early_data": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_target_https_proxy", googleComputeTargetHttpsProxyPolicy)
}
