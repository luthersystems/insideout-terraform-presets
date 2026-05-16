package policy

// googleStorageBucketPolicy curates Layer 2 for `google_storage_bucket`.
//
// Bundle D1 (#491): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Scalars (name, project, location, storage_class,
// encryption KMS key, etc.) use DriftSemanticExact. The CORS leaves
// are flat scalars within the repeated block — per-leaf Exact is the
// right granularity, matching the pattern used by aws_dynamodb_table
// for repeated-block leaves.
//
// Reliable #1479 follow-up: `lifecycle_rule` is now a single
// DriftSemanticWholeList entry (instead of four per-leaf
// `lifecycle_rule.{action.type, action.storage_class, condition.age,
// condition.with_state}` entries). The lifecycle list is order-
// sensitive on the GCS provider side (rules evaluate top-down), so a
// re-ordered or any leaf-level change collapses into a single drift
// banner rather than fanning out to N per-leaf banners. This matches
// the legacy reliable comparator
// (compareGCSBucketAttrs.canonicalizeSnapshotLifecycle) and unblocks
// the Surface B per-type-comparator deletion.
//
// `labels` adopts gcpLabelDriftPolicy() so user-set labels surface as
// drift (per-key `labels.<keyname>` mismatches) while goog-* /
// insideout-import* control-plane and provenance labels are filtered
// out. `effective_labels` and `terraform_labels` stay on tagPolicy()
// — they're computed values that always echo `labels`, so re-emitting
// drift on them would just be noise.
var googleStorageBucketPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"url": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
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
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging.log_bucket": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging.log_object_prefix": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — class and access
	"storage_class": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"force_destroy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"requester_pays": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"uniform_bucket_level_access": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"public_access_prevention": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — versioning, lifecycle, retention
	"versioning.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	// lifecycle_rule — whole-list compare. The list is order-sensitive
	// on the GCS provider side (rules evaluate top-down), so re-ordering
	// or any leaf-level change collapses into one drift banner rather
	// than per-leaf fan-out.
	"lifecycle_rule": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"retention_policy.retention_period": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"retention_policy.is_locked": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"soft_delete_policy.retention_duration_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// CORS — `cors.method`, `cors.origin`, `cors.response_header` are
	// list-shaped leaves on each cors block; whole-list compare keeps
	// the ordering signal.
	"cors.method": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors.origin": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors.response_header": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors.max_age_seconds": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Website + autoclass + rpo
	"website.main_page_suffix": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"website.not_found_page": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"autoclass.enabled": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"rpo": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels — `labels` carries user-set drift signal (per-key
	// `labels.<keyname>` mismatches). `effective_labels` and
	// `terraform_labels` are computed echoes of `labels`; emitting
	// drift on those would just duplicate the signal.
	"labels":           gcpLabelDriftPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_storage_bucket", googleStorageBucketPolicy)
}
