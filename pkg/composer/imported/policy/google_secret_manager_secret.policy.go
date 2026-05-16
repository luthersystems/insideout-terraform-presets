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
// uniformly.
//
// Reliable #1479 follow-up: `labels` adopts gcpLabelDriftPolicy() so
// user-set labels surface as drift (per-key `labels.<keyname>`
// mismatches) while goog-* / insideout-import* control-plane and
// provenance labels are filtered out — matches the legacy reliable
// comparator (compareGoogleSecretManagerSecretAttrs.diffUserLabels)
// so the Surface B per-type-comparator deletion preserves the user-
// facing drift signal. Annotations stay system-only (no curated
// drift signal in the legacy comparator).
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

	// Labels and annotations — `labels` carries user-set drift
	// signal; computed echoes (`effective_labels`, `terraform_labels`)
	// and annotation bags stay system-only.
	"labels":                gcpLabelDriftPolicy(),
	"effective_labels":      tagPolicy(),
	"terraform_labels":      tagPolicy(),
	"annotations":           tagPolicy(),
	"effective_annotations": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_secret_manager_secret", googleSecretManagerSecretPolicy)
}
