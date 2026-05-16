package policy

// awsSecretsmanagerSecretRotationPolicy curates Layer 2 for
// `aws_secretsmanager_secret_rotation`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// Rotation identity: `secret_id` is the primary key (one rotation per
// secret); `rotation_lambda_arn` is the rotator wiring;
// `rotation_rules.*` defines the schedule. Drift on these matters
// because a secret with rotation disabled (or pointed at a stale
// lambda) is a security-posture regression.
//
// Drift bundle (#482): scalar attributes use DriftSemanticExact.
var awsSecretsmanagerSecretRotationPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"secret_id": {
		// Pinned at create — one rotation row per secret.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"rotation_enabled": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — rotator lambda -----------------------------------------
	"rotation_lambda_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — rotation schedule --------------------------------------
	"rotation_rules.automatically_after_days": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"rotation_rules.duration": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"rotation_rules.schedule_expression": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"rotate_immediately": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_secretsmanager_secret_rotation", awsSecretsmanagerSecretRotationPolicy)
}
