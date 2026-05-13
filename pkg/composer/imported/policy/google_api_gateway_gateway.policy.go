package policy

var googleAPIGatewayGatewayPolicy = Map{
	// Identity
	"name":       {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":         {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"gateway_id": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"default_hostname": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditNever,
	},

	// Wiring — parent api_config.
	"api_config": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning
	"display_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_api_gateway_gateway", googleAPIGatewayGatewayPolicy)
}
