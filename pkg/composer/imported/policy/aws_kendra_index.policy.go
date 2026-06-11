package policy

// awsKendraIndexPolicy curates Layer 2 for `aws_kendra_index`.
//
// #801 backfill for the aws/kendra preset (#760), deferred there because
// the PR had no terraform locally to refresh the pinned 6.45.0 provider
// schema, and matching the #787 / #795 AI-stack precedent. A Kendra index
// is the GEN-AI-era enterprise-search / RAG retrieval store: callers query
// it for documents, and Kendra assumes role_arn to write the index's
// CloudWatch logs/metrics. The load-bearing drift surfaces are:
//
//   - role_arn — the IAM identity Kendra assumes to write the index's
//     CloudWatch logs + metrics. Required by the resource; a silent rebind
//     re-purposes the index's IAM identity. Security wiring (the canonical
//     role_arn classification across this corpus).
//   - server_side_encryption_configuration.kms_key_id — the customer-
//     managed KMS key protecting the index's stored documents + embeddings
//     at rest. Immutable (set-on-create), so a change is a replace; a
//     silent rebind points the index's encryption at a different key.
//     Security wiring.
//   - edition — DEVELOPER_EDITION vs ENTERPRISE_EDITION. Immutable, so the
//     provider replaces the (~30-minute-to-provision) index on change; a
//     silent edit reshapes the index's capacity + HA contract.
//   - user_context_policy — ATTRIBUTE_FILTER vs USER_TOKEN: the access-
//     control mode that decides whether query results are filtered by the
//     caller's identity/token. A silent flip changes who can see which
//     documents, so it carries the Security pillar.
//
// The provider exposes a deep document-metadata / capacity-units /
// user-token-configuration tree (JWT/JSON token validation for the
// USER_TOKEN access mode). The JWT validation config's secrets_manager_arn
// is a credential reference (Secrets Manager ARN holding the signing key),
// curated as Redacted Security wiring per the #795 provider_arn precedent;
// the deeper per-mode tuning knobs are left to the conservative codegen-
// only default. This preset emits only the index-level fields (name /
// role_arn / edition / user_context_policy / KMS), so curation targets
// those plus the credential reference that any imported index might carry.
var awsKendraIndexPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — CloudWatch-logging IAM identity -----------------------
	"role_arn": {
		// Silent rebind re-purposes the index's IAM identity.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Encryption — at-rest protection of the stored corpus -----------
	// Immutable (set-on-create); a change forces a replace and points the
	// index's encryption at a different CMK.
	"server_side_encryption_configuration.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Capacity / HA contract — replace-shaped enum -------------------
	"edition": {
		// DEVELOPER_EDITION vs ENTERPRISE_EDITION — immutable; provider
		// replaces the index on change.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Access-control mode — who can see which results ----------------
	// ATTRIBUTE_FILTER vs USER_TOKEN. A silent flip changes whether query
	// results are scoped to the caller's identity, so it is a Security
	// surface even though it is an in-place-mutable knob.
	"user_context_policy": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Credential reference — JWT signing-key store -------------------
	// For the USER_TOKEN access mode, the JWT validation config points at a
	// Secrets Manager secret holding the signing key. A silent rebind
	// trusts a different signing authority. Security wiring, Redacted so
	// the ARN shows for context but is never a raw secret (per the #795
	// provider_arn precedent).
	"user_token_configurations.jwt_token_type_configuration.secrets_manager_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_kendra_index", awsKendraIndexPolicy)
}
