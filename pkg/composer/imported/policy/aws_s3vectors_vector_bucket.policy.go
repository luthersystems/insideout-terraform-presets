package policy

// awsS3vectorsVectorBucketPolicy curates Layer 2 for
// `aws_s3vectors_vector_bucket`.
//
// #787 backfill for the S3 Vectors backbone of the Bedrock Knowledge
// Base RAG stack (#783). An S3 Vectors bucket is the durable store the
// vector indexes live under. The high-value drift surface is the
// server-side encryption configuration (a silent CMK rebind or a flip
// to SSE-S3 changes who can read the embedded corpus) and the
// force_destroy guard (a silent flip to true makes an
// otherwise-protected store deletable). The name is replace-on-change
// identity.
var awsS3vectorsVectorBucketPolicy = Map{
	// Identity ----------------------------------------------------------
	"vector_bucket_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"vector_bucket_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Encryption — who can read the embedded corpus -------------------
	// encryption_configuration is an optional+computed map attribute
	// (sse_algorithm / kms_key_arn). Curated whole so an out-of-band
	// edit to either the algorithm or the CMK binding surfaces as drift.
	"encryption_configuration": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — destroy guard -----------------------------------------
	"force_destroy": {
		// Silent flip to true makes a protected store deletable.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_s3vectors_vector_bucket", awsS3vectorsVectorBucketPolicy)
}
