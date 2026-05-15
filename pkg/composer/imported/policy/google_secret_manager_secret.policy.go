package policy

// googleSecretManagerSecretPolicy curates Layer 2 for
// `google_secret_manager_secret`.
//
// Bundle D2 (#491): DriftSemantic axis is curated on every non-label,
// non-timeouts entry. As with `aws_secretsmanager_secret`, all curated
// fields here are metadata (identity, replication wiring, rotation
// schedule); the secret material lives on
// `google_secret_manager_secret_version.secret_data` (a separate
// resource) and is not curated here, so the Sensitive-leak concern
// that gates `aws_lambda_function.environment.variables` does not
// arise. All curated leaves are scalar — DriftSemanticExact applies
// uniformly. Label and annotation bags stay DriftSemanticNone
// (tagPolicy() zero value); user-author label LabelFilter coverage
// is deferred to the comparator's redacted-mode follow-up (axes.go).
var googleSecretManagerSecretPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"secret_id": {
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

	// Wiring — replication targets and CMEK
	"replication.auto.customer_managed_encryption.kms_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replication.user_managed.replicas.location": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replication.user_managed.replicas.customer_managed_encryption.kms_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"topics.name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — lifecycle / rotation knobs
	"expire_time": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ttl": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"version_destroy_ttl": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"rotation.rotation_period": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"rotation.next_rotation_time": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels and annotations — system-owned. DriftSemantic stays None
	// (tagPolicy() zero value); user-author label drift coverage is the
	// LabelFilter follow-up tracked alongside the axes.go redacted-mode
	// work.
	"labels":                tagPolicy(),
	"effective_labels":      tagPolicy(),
	"terraform_labels":      tagPolicy(),
	"annotations":           tagPolicy(),
	"effective_annotations": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_secret_manager_secret", googleSecretManagerSecretPolicy)
}
