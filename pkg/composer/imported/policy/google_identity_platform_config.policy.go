package policy

var googleIdentityPlatformConfigPolicy = Map{
	// Identity. The Config is a project-scoped singleton named
	// projects/<p>/config; both `name` (full path) and `id` are
	// provider-computed.
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact,
	},

	// Tuning — authorized domains and auto-delete behavior are the
	// primary operator-facing knobs.
	"authorized_domains": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval, DriftSemantic: DriftSemanticWholeList,
	},
	"autodelete_anonymous_users": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe, DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_identity_platform_config", googleIdentityPlatformConfigPolicy)
}
