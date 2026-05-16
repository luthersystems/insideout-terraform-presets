package policy

// awsS3BucketVersioningPolicy curates Layer 2 for `aws_s3_bucket_versioning`.
//
// S3 bucket sub-resource (#482). The TF schema is tiny — a bucket-keyed
// header plus a single versioning_configuration block with two scalars.
// Identity-only curation: `bucket` is the load-bearing identifier and
// the sub-resource cannot be renamed; `id` is provider-stamped. The
// versioning_configuration leaves are operationally meaningful so they
// surface as Tuning with appropriate edit policies.
//
// Drift semantics: versioning state is binary-ish (Enabled / Suspended)
// — Exact equality is the meaningful comparison. MFA Delete is a high-
// risk toggle, so RequiresApproval gates the edit path.
var awsS3BucketVersioningPolicy = Map{
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

	// Tuning — versioning_configuration leaves ------------------------
	"versioning_configuration.status": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"versioning_configuration.mfa_delete": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// MFA header field — required when toggling MFA Delete. SystemOnly
	// because the composer threads the value in from the operator's
	// session, never from chat.
	"mfa": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditSystemOnly,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_s3_bucket_versioning", awsS3BucketVersioningPolicy)
}
