package policy

// googleIdentityPlatformDefaultSupportedIdpConfigPolicy curates Layer 2
// for `google_identity_platform_default_supported_idp_config`.
//
// Companion to googleIdentityPlatformConfigPolicy: the parent Config
// singleton holds project-scoped sign-in settings; each
// DefaultSupportedIdp child holds the per-IDP credentials and enable
// flag for one OAuth provider (google.com, facebook.com, apple.com,
// twitter.com, etc.).
//
// Sensitive credentials (`client_id`, `client_secret`) are tagged
// via tagPolicy() — Hidden + SystemOnly + Redacted — so they never
// surface in chat / UI panes. The enricher's Get call returns
// whatever the API has on file; the emit/persist layers redact at
// write time per decision #36.
var googleIdentityPlatformDefaultSupportedIdpConfigPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"idp_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — the per-IDP enable flag is the canonical drift hook and
	// the obvious user-editable knob.
	"enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Sensitive credentials — Hidden + SystemOnly + Redacted via the
	// shared tagPolicy(). The provider Requires both fields, but
	// exposing them anywhere user-visible would defeat the purpose of
	// the Sensitive flag in the schema.
	"client_id":     tagPolicy(),
	"client_secret": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_identity_platform_default_supported_idp_config", googleIdentityPlatformDefaultSupportedIdpConfigPolicy)
}
