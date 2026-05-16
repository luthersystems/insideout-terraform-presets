package policy

// awsCognitoUserPoolDomainPolicy curates Layer 2 for
// `aws_cognito_user_pool_domain`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// A user-pool domain is the hosted-UI hostname end-users hit during
// authentication. Identity is (id, domain, user_pool_id). The optional
// `certificate_arn` pins which ACM cert serves the custom-domain
// variant (Amazon-prefix domains skip this). Out-of-band changes to
// the cert or pool wiring can silently retarget who can log in.
//
// Drift bundle 10 (#482): scalars use DriftSemanticExact. No tag
// surface — Cognito user-pool domains are not directly tagged.
var awsCognitoUserPoolDomainPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain": {
		// Custom (`auth.example.com`) or Amazon-prefix
		// (`<prefix>.auth.<region>.amazoncognito.com`) hostname. Pinned
		// at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent user pool + cert -------------------------------
	"user_pool_id": {
		// Pointer to the parent aws_cognito_user_pool. Pinned at create.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"certificate_arn": {
		// ACM certificate serving the hosted-UI hostname. Retargeting
		// flips the served cert — RequiresApproval.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Observability — server-side wiring identifiers ------------------
	"cloudfront_distribution": {
		// CF distribution Cognito provisions to serve the hosted UI.
		// Pure observability — the value is server-assigned.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		// Server-bumped on each (re)issuance of the domain cert wiring.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cognito_user_pool_domain", awsCognitoUserPoolDomainPolicy)
}
