package policy

// awsCloudfrontOriginAccessIdentityPolicy curates Layer 2 for
// `aws_cloudfront_origin_access_identity`. Cloud-control-routed
// enrichment already produces typed Attrs; this map adds the curated
// surface to flip the type from Enrichable to DriftDetectable.
//
// A CloudFront Origin Access Identity (OAI) is the legacy principal used
// to lock S3 origins so they only serve via the CloudFront distribution
// (the modern replacement is OAC — Origin Access Control — but OAI is
// still in active use). Identity is (id, iam_arn, s3_canonical_user_id).
// The single operator-controlled axis is `comment` (free-text marker).
// Etag bumps on every update — drift on it flags an out-of-band edit.
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact. No tag surface
// — OAIs are CloudFront-global resources and don't take user tags.
var awsCloudfrontOriginAccessIdentityPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"iam_arn": {
		// Auto-generated IAM principal ARN used in S3 bucket policies.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"s3_canonical_user_id": {
		// Canonical-user-id form of the principal — used in S3 ACLs.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"cloudfront_access_identity_path": {
		// "origin-access-identity/cloudfront/<id>" — used in distribution
		// origin blocks.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"caller_reference": {
		// Provider-generated idempotency token captured at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"etag": {
		// Bumps on every update — drift here flags an out-of-band edit.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Operator-controlled label ---------------------------------------
	"comment": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cloudfront_origin_access_identity", awsCloudfrontOriginAccessIdentityPolicy)
}
