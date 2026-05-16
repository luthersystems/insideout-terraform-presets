package policy

// awsCognitoIdentityProviderPolicy curates Layer 2 for
// `aws_cognito_identity_provider`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// A Cognito identity provider record federates an external IdP
// (SAML / OIDC / Facebook / Google / SignInWithApple / LoginWithAmazon)
// into a Cognito user pool. Identity is (user_pool_id, provider_name);
// provider_type pins the federation protocol. `provider_details` holds
// the IdP-specific config (client_id, client_secret, metadata URL, …);
// `attribute_mapping` joins IdP claims onto Cognito user attributes.
// Drift on any of these is high-signal — silently flipping a SAML
// metadata URL or an OIDC client_secret rotates which IdP fronts the
// pool.
//
// Drift bundle 9 (#482): scalars use DriftSemanticExact; map-shaped
// attributes (provider_details, attribute_mapping) and the
// idp_identifiers list use DriftSemanticWholeList so a missing/extra
// entry is one diff entry, not N.
var awsCognitoIdentityProviderPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"provider_name": {
		// Per-pool unique IdP name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"provider_type": {
		// SAML | OIDC | Facebook | Google | SignInWithApple | LoginWithAmazon.
		// Pinned at create — flipping protocol means rebuilding the IdP.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent user pool ---------------------------------------
	"user_pool_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — IdP-specific configuration -----------------------------
	"provider_details": {
		// Map of IdP-protocol-specific knobs (client_id, client_secret,
		// metadata URL, OIDC issuer, authorize_scopes, …). The
		// security-critical blob; out-of-band rotation surfaces here.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"attribute_mapping": {
		// IdP-claim → Cognito-attribute join. Drift on the mapping
		// silently changes which claim populates which attribute.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"idp_identifiers": {
		// Optional set of human-friendly IdP aliases used by the
		// AdminQueryUser API. Whole-list compare.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
}

func init() {
	Register("aws_cognito_identity_provider", awsCognitoIdentityProviderPolicy)
}
