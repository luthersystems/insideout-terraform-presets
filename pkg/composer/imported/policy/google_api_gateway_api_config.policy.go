package policy

var googleAPIGatewayAPIConfigPolicy = Map{
	// Identity
	"name":          {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":            {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"api_config_id": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"api_config_id_prefix": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"service_config_id": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever,
	},

	// Wiring — parent API.
	"api": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning
	"display_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},

	// Spec / config payloads — operator-controlled, sensitivity not
	// flagged (specs are public-API contracts; credentials live in the
	// backend service account, not in the spec).
	"openapi_documents.document.contents": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
	},
	"openapi_documents.document.path": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"managed_service_configs.contents": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"managed_service_configs.path": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"grpc_services.file_descriptor_set.contents": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"grpc_services.source.contents": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// Backend identity — wired to a service account.
	"gateway_config.backend_config.google_service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_api_gateway_api_config", googleAPIGatewayAPIConfigPolicy)
}
