package policy

var googleServiceAccountPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	// account_id is the local part of the SA email; it's the durable
	// identifier (cannot be changed in place).
	"account_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"email": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"unique_id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},
	"member": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},

	// Tuning
	"display_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"disabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"create_ignore_already_exists": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_service_account", googleServiceAccountPolicy)
}
