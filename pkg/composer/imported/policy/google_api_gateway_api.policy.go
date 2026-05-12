package policy

var googleAPIGatewayAPIPolicy = Map{
	// Identity
	"name":   {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":     {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"api_id": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"create_time": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},
	"managed_service": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever,
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
	Register("google_api_gateway_api", googleAPIGatewayAPIPolicy)
}
