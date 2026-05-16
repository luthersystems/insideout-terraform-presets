package policy

// awsCloudfrontFunctionPolicy curates Layer 2 for
// `aws_cloudfront_function`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// A CloudFront function is a JS snippet run at the edge on viewer
// request / response. Identity is (arn, id, name). `code` is the
// load-bearing payload — exact-string drift catches out-of-band code
// edits in the AWS console. `runtime` pins the JS engine (cloudfront-js-1.0
// / cloudfront-js-2.0); `etag` / `live_stage_etag` are stable handles
// that the provider uses to detect publish-version drift.
//
// Drift bundle 10 (#482): scalars use DriftSemanticExact. No tag
// surface — CloudFront functions are not directly tagged.
var awsCloudfrontFunctionPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Function name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Runtime + code — the load-bearing surface -----------------------
	"runtime": {
		// Pins JS engine version (cloudfront-js-1.0 / cloudfront-js-2.0).
		// Out-of-band changes silently flip the execution semantics.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"code": {
		// JS source. Exact-string drift catches out-of-band edits to the
		// edge-runtime logic — security-sensitive (rewrites, redirects,
		// auth header munging).
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Publish + version handles ---------------------------------------
	"etag": {
		// Server-computed handle for the latest version. Drift flags an
		// out-of-band publish.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"status": {
		// UNPUBLISHED / UNASSOCIATED / DEPLOYED; observability surface.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cloudfront_function", awsCloudfrontFunctionPolicy)
}
