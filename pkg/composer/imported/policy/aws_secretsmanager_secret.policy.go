package policy

var awsSecretsmanagerSecretPolicy = Map{
	// Identity
	"arn":  {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},

	// Wiring (encryption + replication targets)
	"kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"replica.region": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"replica.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"recovery_window_in_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// Resource policy is an IAM JSON document — visible to the interactive
	// agent but each change requires explicit confirmation against the plan.
	"policy": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_secretsmanager_secret", awsSecretsmanagerSecretPolicy)
}
