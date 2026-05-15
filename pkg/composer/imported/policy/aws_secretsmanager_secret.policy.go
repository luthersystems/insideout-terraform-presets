package policy

// awsSecretsmanagerSecretPolicy curates Layer 2 for `aws_secretsmanager_secret`.
//
// Bundle D2 (#491): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. All curated fields here are metadata about the
// secret (ARN/name identity, KMS encryption wiring, replication targets,
// IAM resource policy, recovery window, description) — none of them
// carry the secret material itself. The actual secret payload lives on
// `aws_secretsmanager_secret_version.secret_string` (a separate
// resource) and is not curated in this map, so the Sensitive-leak
// concern that gates `aws_lambda_function.environment.variables` does
// not arise. All curated leaves are scalar; DriftSemanticExact is the
// meaningful comparison for each. Tag bags stay DriftSemanticNone
// (tagPolicy() zero value).
var awsSecretsmanagerSecretPolicy = Map{
	// Identity
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring (encryption + replication targets)
	"kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.region": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"recovery_window_in_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Resource policy is an IAM JSON document — visible to the interactive
	// agent but each change requires explicit confirmation against the plan.
	// Exact comparison surfaces canonicalization-induced churn intentionally;
	// the diff screen renders the doc with structural diff.
	"policy": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_secretsmanager_secret", awsSecretsmanagerSecretPolicy)
}
