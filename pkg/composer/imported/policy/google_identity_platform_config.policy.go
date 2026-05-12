package policy

var googleIdentityPlatformConfigPolicy = Map{
	// Identity. The Config is a project-scoped singleton named
	// projects/<p>/config; both `name` (full path) and `id` are
	// provider-computed.
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning — authorized domains and auto-delete behavior are the
	// primary operator-facing knobs.
	"authorized_domains": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"autodelete_anonymous_users": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_identity_platform_config", googleIdentityPlatformConfigPolicy)
}
