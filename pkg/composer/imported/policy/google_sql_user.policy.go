package policy

var googleSqlUserPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact,
	},
	"host": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent instance.
	"instance": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact,
	},

	// Tuning — user type.
	"type": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact,
	},
	"deletion_policy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// Password is a bootstrap secret. The provider stores it sensitively
	// and the composer/import flow shouldn't surface raw values.
	"password": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityHidden,
		Edit: EditSystemOnly, Sensitivity: SensitivitySensitive,
	},

	// SQL Server-specific user details — server roles + nested config.
	// Single-row curation for the most commonly-tuned knob.
	"sql_server_user_details.disabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_sql_user", googleSqlUserPolicy)
}
