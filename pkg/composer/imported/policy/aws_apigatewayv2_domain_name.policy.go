package policy

// awsApigatewayv2DomainNamePolicy curates Layer 2 for
// `aws_apigatewayv2_domain_name`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// An APIGW v2 domain name is the public custom-domain endpoint that maps
// onto APIs via aws_apigatewayv2_api_mapping. Identity is (id, arn,
// domain_name). The nested `domain_name_configuration` block carries the
// ACM certificate + endpoint type and is intentionally left uncurated —
// block-level drift is a follow-up. Top-level scalar drift covers the
// highest-signal axis (domain replaced).
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy(). Timeouts use timeoutsPolicy().
var awsApigatewayv2DomainNamePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_name": {
		// Public custom-domain hostname. Pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"api_mapping_selection_expression": {
		// Provider-computed expression used to pick the API behind this
		// domain.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_apigatewayv2_domain_name", awsApigatewayv2DomainNamePolicy)
}
