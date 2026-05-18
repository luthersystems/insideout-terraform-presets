package policy

// googleIAMWorkloadIdentityPoolProviderPolicy curates Layer 2 for
// `google_iam_workload_identity_pool_provider`. This is the
// security-load-bearing resource in the WIF stack: the provider's
// `attribute_condition` is a CEL expression that controls WHICH
// federated tokens may exchange for GCP service-account credentials.
// An out-of-band relaxation (e.g. dropping a repository / branch / OIDC
// audience guard) is precisely the attack-shaped event drift detection
// exists to catch. Same goes for `attribute_mapping` (rewrites the
// federated token's claims into Google's IAM principal vocabulary;
// a silent edit can re-route every workflow run to a different SA) and
// `oidc.issuer_uri` (the only thing standing between Google and an
// attacker-controlled OIDC issuer issuing arbitrary tokens).
//
// The gcp/github_actions preset sets attribute_condition to
//   assertion.repository == "<owner>/<repo>"
// and attribute_mapping to
//   google.subject = assertion.sub
//   attribute.repository = assertion.repository
//   attribute.actor = assertion.actor
// — every byte of those values is load-bearing for keyless deploy
// security.
//
// Identity is (project, workload_identity_pool_id, workload_identity_
// pool_provider_id). `name` is the provider-computed fully-qualified
// path; `state` is the lifecycle state.
//
// Bundle (#607): part of the gcp/github_actions full-fidelity follow-up
// for the v1 preset (#605). Codegen-only — see gcpCodegenOnlyTypes in
// pkg/insideout-import/registry/registry.go for the rationale on
// shipping drift policy before the CAI discoverer hookup (#608).
var googleIAMWorkloadIdentityPoolProviderPolicy = Map{
	// Identity ---------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// projects/<project>/locations/global/workloadIdentityPools/<p>/
		// providers/<pid>. Provider-computed identity.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"workload_identity_pool_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"workload_identity_pool_provider_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"state": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Security-critical surface ---------------------------------------
	"attribute_condition": {
		// CEL expression gating WHICH federated tokens can exchange for
		// SA credentials. The single most security-load-bearing field
		// on the entire WIF stack. Any out-of-band relaxation is a
		// real attack-shaped event.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"attribute_mapping": {
		// Map of federated token claims → Google IAM principal claims.
		// A silent rewrite re-routes every authenticated workflow to a
		// different principal — equivalent to silently swapping role
		// assumption rules in AWS. WholeList so an add / remove / change
		// to any single mapping surfaces as one diff entry.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"disabled": {
		// Per-provider kill-switch. Less catastrophic than the
		// pool-level disabled flag but still security-pillar.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// OIDC sub-block — the github_actions preset's flavor ------------
	"oidc.issuer_uri": {
		// The OIDC issuer URL. For GitHub Actions this is
		// https://token.actions.githubusercontent.com. A silent change
		// would re-route trust to an attacker-controlled issuer.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"oidc.allowed_audiences": {
		// Optional audience whitelist on incoming OIDC tokens. When
		// non-empty it's load-bearing — a missing-or-extra audience is
		// a real security event. WholeList for set semantics.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"oidc.jwks_json": {
		// Inline JWKS for the OIDC issuer when the issuer doesn't host
		// a well-known endpoint. Cryptographic material — a silent
		// change is real security drift.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// AWS sub-block (alternative provider flavor) --------------------
	"aws.account_id": {
		// AWS account ID this provider federates from. Load-bearing
		// trust boundary identical in spirit to oidc.issuer_uri.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// SAML sub-block (alternative provider flavor) -------------------
	"saml.idp_metadata_xml": {
		// SAML IDP metadata XML containing the issuer's signing keys
		// and binding URLs. Cryptographic + trust material.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// X509 sub-block (mTLS provider flavor) --------------------------
	"x509.trust_store.trust_anchors.pem_certificate": {
		// Root CA PEM that anchors trust for the workload's mTLS
		// certificates. Cryptographic trust anchor.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"x509.trust_store.intermediate_cas.pem_certificate": {
		// Intermediate CA PEM (optional chain). Same trust-anchor
		// posture as trust_anchors.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning -----------------------------------------------------------
	"display_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_iam_workload_identity_pool_provider", googleIAMWorkloadIdentityPoolProviderPolicy)
}
