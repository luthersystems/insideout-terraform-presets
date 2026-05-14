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
var awsS3BucketPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"bucket": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"bucket_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"bucket_domain_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"bucket_regional_domain_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"hosted_zone_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"website_domain": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"website_endpoint": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},

	// Tuning — top-level knobs -----------------------------------------
	"acceleration_status": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"acl": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"force_destroy": {
		// Destructive flag — UI-visible so the management view shows it,
		// but only system code (the importer + composer) writes it.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditSystemOnly,
	},
	"object_lock_enabled": {
		// Top-level boolean — set once at bucket creation; flipping it
		// after the fact requires replacement.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"request_payer": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Wiring — bucket policy doc ---------------------------------------
	"policy": {
		// IAM-style JSON document. Riley can propose changes but apply
		// requires explicit approval against the diff.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// Versioning --------------------------------------------------------
	"versioning.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"versioning.mfa_delete": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// Logging -----------------------------------------------------------
	"logging.target_bucket": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"logging.target_prefix": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Object lock -------------------------------------------------------
	"object_lock_configuration.object_lock_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},

	// Replication -------------------------------------------------------
	"replication_configuration.role": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Server-side encryption -------------------------------------------
	"server_side_encryption_configuration.rule.apply_server_side_encryption_by_default.kms_master_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
	},
	"server_side_encryption_configuration.rule.apply_server_side_encryption_by_default.sse_algorithm": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"server_side_encryption_configuration.rule.bucket_key_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},

	// Tags (system-managed bag) ----------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_s3_bucket", awsS3BucketPolicy)
}
