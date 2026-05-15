package policy

// awsS3BucketServerSideEncryptionConfigurationPolicy curates Layer 2
// for `aws_s3_bucket_server_side_encryption_configuration`.
//
// S3 bucket sub-resource (#482). The TF schema is a bucket header plus
// a list of rule blocks. Each rule has an
// apply_server_side_encryption_by_default sub-block carrying the
// algorithm + KMS key, and a bucket_key_enabled flag. The KMS key ARN
// is curated as a Wiring leaf (cross-resource reference) so
// RelationshipOnly edits route through the wiring path. The algorithm
// and bucket_key flag are Tuning leaves.
//
// Drift semantics: encryption posture is high-signal — a silent
// downgrade from aws:kms to AES256 (or KMS key swap to a less-scoped
// key) is a real security event. DriftSemanticExact on every leaf.
var awsS3BucketServerSideEncryptionConfigurationPolicy = Map{
	// Identity ----------------------------------------------------------
	"bucket": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"expected_bucket_owner": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — KMS key reference ---------------------------------------
	"rule.apply_server_side_encryption_by_default.kms_master_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — algorithm + bucket-key flag -----------------------------
	"rule.apply_server_side_encryption_by_default.sse_algorithm": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"rule.bucket_key_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_s3_bucket_server_side_encryption_configuration", awsS3BucketServerSideEncryptionConfigurationPolicy)
}
