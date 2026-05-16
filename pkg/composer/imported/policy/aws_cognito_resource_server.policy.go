package policy

// awsCognitoResourceServerPolicy curates Layer 2 for
// `aws_cognito_resource_server`.
//
// A Cognito resource server registers an OAuth2 protected resource
// (typically a backend API) within a Cognito user pool, and declares
// the custom OAuth scopes that clients can request via the
// user-pool-client `allowed_oauth_scopes`. Identity is
// (identifier, user_pool_id). The `scope` block is the
// security-relevant surface — adding / removing scopes silently
// expands or narrows what JWTs may carry.
//
// Drift bundle 12 (#482): scalars use DriftSemanticExact. No tag
// surface.
var awsCognitoResourceServerPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"identifier": {
		// OAuth audience identifier; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Display name within the user pool.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent user pool ---------------------------------------
	"user_pool_id": {
		// Pointer to the parent aws_cognito_user_pool. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Security-critical OAuth scope surface ---------------------------
	"scope.scope_name": {
		// Per-scope name (e.g. "read", "write"). Drift = silent
		// expansion or removal of permissible JWT scopes.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"scope.scope_description": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"scope_identifiers": {
		// Provider-derived flat list of fully-qualified scope ids
		// (identifier + "/" + scope_name).
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cognito_resource_server", awsCognitoResourceServerPolicy)
}
