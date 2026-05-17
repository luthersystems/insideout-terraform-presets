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
// meaningful comparison for each.
//
// #568: `tags` / `tags_all` adopt awsTagDriftPolicy() — symmetric
// with google_secret_manager_secret's gcpLabelDriftPolicy() adoption.
// User-set tag drift surfaces as per-key `tags.<key>` mismatches;
// AWS-managed prefixes are filtered.
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
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.region": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"recovery_window_in_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Resource policy is an IAM JSON document — visible to the interactive
	// agent but each change requires explicit confirmation against the plan.
	// Exact comparison surfaces canonicalization-induced churn intentionally;
	// the diff screen renders the doc with structural diff.
	"policy": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags — awsTagDriftPolicy() (#568): per-key user-tag drift
	// surfaces; AWS-managed prefixes filtered.
	"tags":     awsTagDriftPolicy(),
	"tags_all": awsTagDriftPolicy(),
}

func init() {
	Register("aws_secretsmanager_secret", awsSecretsmanagerSecretPolicy)
}
