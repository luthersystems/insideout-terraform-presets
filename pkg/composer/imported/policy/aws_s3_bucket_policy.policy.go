package policy

// awsS3BucketPolicyPolicy curates Layer 2 for `aws_s3_bucket_policy`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An S3 bucket policy is the resource-policy IAM document attached to a
// bucket. Identity is (bucket, id). `policy` is the security-critical
// JSON document — who can Get/Put/Delete objects, cross-account access
// grants, public-access carve-outs. Drift here is the highest-signal
// regression on object security.
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact. No tag
// surface — bucket policies are not tagged (the parent bucket carries
// tags). The policy document is compared as opaque text; canonical-JSON
// normalization happens at a higher diff layer.
var awsS3BucketPolicyPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent bucket ------------------------------------------
	"bucket": {
		// Pointer to the parent aws_s3_bucket. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Security-critical payload ---------------------------------------
	"policy": {
		// IAM policy JSON. Security boundary — drift means out-of-band
		// access-grant changes (cross-account, public access, principal
		// rewrites).
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_s3_bucket_policy", awsS3BucketPolicyPolicy)
}
