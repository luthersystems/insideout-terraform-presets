package policy

// googleServiceAccountIAMBindingPolicy curates the Layer 2 axes for
// the google_service_account_iam_binding resource. The `members` list
// is RoleTuning + Security-pillar: it defines exactly which principals
// can impersonate the underlying service account. For the
// gcp/github_actions WIF stack the canonical member is
//
//	principalSet://iam.googleapis.com/projects/<num>/locations/global/
//	  workloadIdentityPools/<pool>/attribute.repository/<owner>/<repo>
//
// — adding any other principal to that list grants long-term
// impersonation rights, which is exactly the attack-shaped event drift
// detection exists to catch.
//
// Drift bundle mirrors the canonical IAM-binding template
// (google_kms_crypto_key_iam_binding, google_secret_manager_secret_iam_
// binding): `role` is Exact + Security, `members` is WholeList +
// Security (set semantics — order-insensitive but a missing-or-extra
// principal surfaces as a single mismatch). `service_account_id` is
// Exact for completeness; id / etag stay DriftSemantic=None.
//
// Bundle (#607): part of the gcp/github_actions full-fidelity follow-up
// for the v1 preset (#605). Codegen-only — see gcpCodegenOnlyTypes in
// pkg/insideout-import/registry/registry.go for the rationale on
// shipping drift policy before the CAI discoverer hookup (#608).
var googleServiceAccountIAMBindingPolicy = Map{
	"id":   {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"etag": {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"service_account_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"role": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"members": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeInPlace,
		DriftSemantic: DriftSemanticWholeList,
	},
}

func init() {
	Register("google_service_account_iam_binding", googleServiceAccountIAMBindingPolicy)
}
