package policy

// awsS3BucketPublicAccessBlockPolicy curates Layer 2 for
// `aws_s3_bucket_public_access_block`.
//
// S3 bucket sub-resource (#482). Four booleans control whether the
// bucket can be made public via ACLs / policies. All four are
// security-load-bearing — silent drift here is the canonical "S3
// bucket accidentally went public" signal. Each boolean is curated as
// Tuning + PillarSecurity + RequiresApproval so the interactive agent
// can surface a proposed change but never apply it directly.
//
// Recommended: all four = true. The TF resource exists primarily to
// pin those values; presets in this repo enable them by default.
var awsS3BucketPublicAccessBlockPolicy = Map{
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

	// Tuning — public-access block booleans ----------------------------
	"block_public_acls": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"block_public_policy": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ignore_public_acls": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"restrict_public_buckets": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_s3_bucket_public_access_block", awsS3BucketPublicAccessBlockPolicy)
}
