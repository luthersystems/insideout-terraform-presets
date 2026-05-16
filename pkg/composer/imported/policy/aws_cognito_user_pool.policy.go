package policy

// awsCognitoUserPoolPolicy curates Layer 2 for `aws_cognito_user_pool`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Cognito user pool is the top-level identity tenant container.
// Identity is (id, name, arn). Security-relevant tuning includes the
// password policy, MFA configuration, account-recovery method set,
// admin-create-user constraints, and the user-pool add-ons (advanced
// security mode). Drift on any of these silently re-shapes the auth
// surface (e.g. flipping MFA from ON to OFF, weakening password
// length, enabling self-service signup on a pool that was admin-only).
//
// Drift bundle 13 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy(). Closes the last AWS Enrichable→Drift gap (push to 100%).
// The nested `schema` block (custom attributes) historically blocked
// codegen — resolved bundle 13 by disambiguating `<Type>Schema`
// collisions to `<Type>SchemaNested`. The nested blocks themselves are
// left uncurated for now — the top-level scalars + the
// password_policy / admin_create_user_config / account_recovery_setting
// sub-paths cover the security-critical drift surface.
var awsCognitoUserPoolPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// User-pool name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"endpoint": {
		// Provider-derived user-pool API endpoint.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"custom_domain": {
		// Wired by aws_cognito_user_pool_domain. Provider-derived here.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain": {
		// Provider-derived Cognito-hosted UI domain.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — username/alias schema (pinned at create) ----------------
	"alias_attributes": {
		// Which attributes (email / phone_number / preferred_username) may
		// be used as alternate sign-in identifiers. Pinned at create.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"username_attributes": {
		// email / phone_number — the primary sign-in identifier. Pinned at
		// create.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"auto_verified_attributes": {
		// email / phone_number — which contact channels Cognito will
		// auto-verify (drives confirmation flow).
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning — MFA + advanced security --------------------------------
	"mfa_configuration": {
		// OFF | ON | OPTIONAL. The load-bearing MFA enforcement axis;
		// flipping ON → OFF silently disables MFA for all users.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"deletion_protection": {
		// ACTIVE | INACTIVE — guards against accidental destroy of the
		// pool (and the loss of every user identity it holds).
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — password policy ----------------------------------------
	"password_policy.minimum_length": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"password_policy.require_lowercase": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"password_policy.require_uppercase": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"password_policy.require_numbers": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"password_policy.require_symbols": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"password_policy.temporary_password_validity_days": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — admin-create-user constraints --------------------------
	"admin_create_user_config.allow_admin_create_user_only": {
		// True ⇒ self-service signup disabled. Drift to false silently
		// opens self-service signup on a pool that was admin-only.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — account-recovery ---------------------------------------
	"account_recovery_setting.recovery_mechanism": {
		// Ordered set of (name, priority) — verified_email /
		// verified_phone_number / admin_only. Drift silently re-routes
		// the password-reset channel.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning — user-pool add-ons (advanced security mode) -------------
	"user_pool_add_ons.advanced_security_mode": {
		// OFF | AUDIT | ENFORCED — Cognito's risk-based / anomaly-
		// detection axis. Flipping ENFORCED → OFF silently disables
		// adaptive auth.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_cognito_user_pool", awsCognitoUserPoolPolicy)
}
