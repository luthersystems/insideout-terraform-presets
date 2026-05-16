package policy

// awsS3BucketOwnershipControlsPolicy curates Layer 2 for
// `aws_s3_bucket_ownership_controls`.
//
// S3 bucket sub-resource (#482). The schema is two fields: a bucket
// header and a `rule.object_ownership` enum (BucketOwnerPreferred /
// ObjectWriter / BucketOwnerEnforced). BucketOwnerEnforced disables
// ACLs entirely and is the AWS recommended default — flipping the value
// has real security consequences, so the rule edit requires approval.
var awsS3BucketOwnershipControlsPolicy = Map{
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

	// Tuning — object ownership enum -----------------------------------
	"rule.object_ownership": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_s3_bucket_ownership_controls", awsS3BucketOwnershipControlsPolicy)
}
