package policy

// googleSqlUserPolicy curates Layer 2 for `google_sql_user`. All
// curated leaves are scalar (user name, host, parent instance,
// user-type enum, deletion policy, sql_server disabled toggle), so
// DriftSemanticExact is the meaningful comparison. The `password`
// bootstrap secret stays DriftSemanticNone — sensitive value, the
// comparator never sees the cleartext.
var googleSqlUserPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"host": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent instance.
	"instance": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — user type.
	"type": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"deletion_policy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
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
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_sql_user", googleSqlUserPolicy)
}
