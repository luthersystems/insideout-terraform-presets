package policy

// googleStorageBucketPolicy curates Layer 2 for `google_storage_bucket`.
//
// Bundle D1 (#491): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Scalars (name, project, location, storage_class,
// encryption KMS key, etc.) use DriftSemanticExact. The CORS and
// lifecycle leaves enumerated here are flat scalars within the
// repeated block — per-leaf Exact is the right granularity, matching
// the pattern used by aws_dynamodb_table for repeated-block leaves.
// The label maps use the tagPolicy() default (DriftSemanticNone) — the
// label-bag drift problem (filtering goog-*) is handled at a higher
// surface; per-field LabelFilter on `labels` is the natural next step
// when we promote label drift detection into the comparator, and is
// deferred to a follow-up.
var googleStorageBucketPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"url": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
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
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring (encryption + cross-bucket logging target)
	"encryption.default_kms_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging.log_bucket": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging.log_object_prefix": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — class and access
	"storage_class": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"force_destroy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"requester_pays": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"uniform_bucket_level_access": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"public_access_prevention": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — versioning, lifecycle, retention
	"versioning.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.action.type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.action.storage_class": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.condition.age": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.condition.with_state": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"retention_policy.retention_period": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"retention_policy.is_locked": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"soft_delete_policy.retention_duration_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// CORS — `cors.method`, `cors.origin`, `cors.response_header` are
	// list-shaped leaves on each cors block; whole-list compare keeps
	// the ordering signal.
	"cors.method": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors.origin": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors.response_header": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors.max_age_seconds": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Website + autoclass + rpo
	"website.main_page_suffix": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"website.not_found_page": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"autoclass.enabled": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"rpo": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels — uniformly system-owned. DriftSemantic stays None for
	// now (tagPolicy() default); the goog-* filtering needed for GCP
	// label drift is exercised via DriftSemanticLabelFilter on policies
	// that have a curated label-drift signal.
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_storage_bucket", googleStorageBucketPolicy)
}
