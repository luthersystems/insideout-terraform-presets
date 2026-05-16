package policy

// awsS3BucketPolicy is the hand-curated Layer 2 field-policy map for
// `aws_s3_bucket`. It mirrors the source-of-truth TS projection in
// reliable's `lib/stack/imported/aws_s3_bucket.policy.ts`; that file's
// `tests/imported-policy-projection.test.ts` locks the two against
// silent drift.
//
// Coverage decision (slice #1346): only configurable fields are
// curated. Computed-only fields (arn, bucket_domain_name, hosted_zone_id,
// region, website_domain, website_endpoint, id) are surfaced as
// Identity / Never / UIVisible so the diff screen can render them as
// read-only context without making them agent-editable.
//
// Identity vs Wiring on top-level: `bucket` is the S3 primary key and
// is `AlwaysReplace`, which the composer's drift comparator depends on
// to surface the recreate-vs-update warning. `bucket_prefix` shares
// that semantic (Terraform picks one or the other; either way changing
// it forces replacement).
//
// Bundle D1 (#491): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Identity ARNs / IDs use Exact equality so a
// renamed-out-of-band bucket still surfaces. Configurable scalars use
// Exact; the cross-resource wiring leaves (KMS key ARN, target bucket,
// replication role) also use Exact — a value diff there is real drift,
// not provider noise.
//
// #568: `tags` / `tags_all` adopt awsTagDriftPolicy() so user-set
// tags (notably the canonical `Project` tag the InsideOut inspector
// uses to attribute resources — CLAUDE.md "Project tag is required on
// every taggable AWS resource") surface as per-key `tags.<key>` drift
// when stripped out-of-band. AWS-managed prefixes (`aws:`, `eks:`,
// `kubernetes.io/`, etc.) are filtered. S3 buckets are stable,
// low-churn, customer-owned — the right shape for tag drift.
//
// Depth-pass extras (#482 follow-up): adds a curated sub-set of the
// deprecated nested-config sub-paths the AWS v4+ provider keeps for
// backward compat (`cors_rule.*`, `website.*`, `lifecycle_rule.*`,
// `grant.*`). Modern stacks should prefer the separate
// `aws_s3_bucket_cors_configuration` / `_website_configuration` /
// `_lifecycle_configuration` resources (already curated in their own
// policy files), but legacy customer state surfaced through the
// importer still uses these inline blocks; tagging them gives drift
// detection on those state files.
var awsS3BucketPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"bucket": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"bucket_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"bucket_domain_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"bucket_regional_domain_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"hosted_zone_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"website_domain": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"website_endpoint": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — top-level knobs -----------------------------------------
	"acceleration_status": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"acl": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"force_destroy": {
		// Destructive flag — UI-visible so the management view shows it,
		// but only system code (the importer + composer) writes it.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditSystemOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"object_lock_enabled": {
		// Top-level boolean — set once at bucket creation; flipping it
		// after the fact requires replacement.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"request_payer": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — bucket policy doc ---------------------------------------
	"policy": {
		// IAM-style JSON document. The interactive agent can propose
		// changes but apply requires explicit approval against the diff.
		// Exact comparison here means whitespace / key-order changes from
		// the provider's canonicalization will surface; that's intentional,
		// the diff screen renders the doc with structural diff.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Versioning --------------------------------------------------------
	"versioning.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"versioning.mfa_delete": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Logging -----------------------------------------------------------
	"logging.target_bucket": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"logging.target_prefix": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Object lock -------------------------------------------------------
	"object_lock_configuration.object_lock_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Replication -------------------------------------------------------
	"replication_configuration.role": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Server-side encryption -------------------------------------------
	"server_side_encryption_configuration.rule.apply_server_side_encryption_by_default.kms_master_key_id": {
		// KMS key ARN is a scalar identifier — Exact equality. A diff
		// here means the bucket's default encryption key changed out of
		// band, which is a real security signal.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"server_side_encryption_configuration.rule.apply_server_side_encryption_by_default.sse_algorithm": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"server_side_encryption_configuration.rule.bucket_key_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// CORS (deprecated inline) ------------------------------------------
	"cors_rule.allowed_methods": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors_rule.allowed_origins": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors_rule.allowed_headers": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors_rule.expose_headers": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cors_rule.max_age_seconds": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Website (deprecated inline) --------------------------------------
	"website.index_document": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"website.error_document": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"website.redirect_all_requests_to": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"website.routing_rules": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Lifecycle (deprecated inline) -------------------------------------
	"lifecycle_rule.id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.prefix": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.abort_incomplete_multipart_upload_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.expiration.days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.expiration.expired_object_delete_marker": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"lifecycle_rule.noncurrent_version_expiration.days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Grant (deprecated; modern stacks use bucket policy + ACL) ---------
	"grant.id": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"grant.permissions": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"grant.type": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags — adopt awsTagDriftPolicy() (#568): user-set tag drift
	// surfaces as per-key `tags.<key>` mismatches; AWS-managed
	// prefixes (`aws:`, `eks:`, `kubernetes.io/`, etc.) are filtered.
	"tags":     awsTagDriftPolicy(),
	"tags_all": awsTagDriftPolicy(),
}

func init() {
	Register("aws_s3_bucket", awsS3BucketPolicy)
}
