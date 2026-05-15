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
// read-only context without making them Riley-editable.
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
// not provider noise. Tag bags stay DriftSemanticNone (tagPolicy() zero
// value) because they're system-managed.
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
		// IAM-style JSON document. Riley can propose changes but apply
		// requires explicit approval against the diff. Exact comparison
		// here means whitespace / key-order changes from the provider's
		// canonicalization will surface; that's intentional, the diff
		// screen renders the doc with structural diff.
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

	// Tags (system-managed bag) ----------------------------------------
	// DriftSemantic stays None — tag drift is provider noise; the diff
	// surface filters tags at a higher layer.
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_s3_bucket", awsS3BucketPolicy)
}
