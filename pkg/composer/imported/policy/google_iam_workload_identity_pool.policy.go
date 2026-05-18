package policy

// googleIAMWorkloadIdentityPoolPolicy curates Layer 2 for
// `google_iam_workload_identity_pool`. The WIF pool is the parent
// trust boundary for keyless federation — every workload identity
// provider hangs off the pool, and the `disabled` flag is the global
// kill-switch for every downstream OIDC / SAML / AWS attestation
// flow. A silent flip from disabled=false to disabled=true would
// black-hole every federated workflow; the reverse re-opens trust.
// Both are real security-pillar events.
//
// Identity is (project, workload_identity_pool_id) — pinned at create
// (the GCP API does not support renaming a pool). `name` is the
// fully-qualified resource path GCP computes from those two; it's a
// stable identity surface, not user-editable.
//
// `description` and `display_name` are operator-facing tags. Treated
// as ChatSafe so the InsideOut interactive agent can re-author them
// without an approval step. DriftSemantic=Exact so an out-of-band
// console rename surfaces as a drift event (low-severity, but worth
// the diff entry — operators sometimes use display_name to encode
// rotation state or ownership and we want to spot that).
//
// `state` is provider-computed (lifecycle state — ACTIVE / DELETED /
// etc.) and not user-editable. Tracked under Identity for drift.
//
// Bundle (#607): part of the gcp/github_actions full-fidelity follow-up
// for the v1 preset (#605). Codegen-only — see gcpCodegenOnlyTypes in
// pkg/insideout-import/registry/registry.go for the rationale on
// shipping drift policy before the CAI discoverer hookup (#608).
var googleIAMWorkloadIdentityPoolPolicy = Map{
	// Identity ---------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// projects/<project>/locations/global/workloadIdentityPools/<pool_id>.
		// Provider-computed; identity but immutable.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"workload_identity_pool_id": {
		// User-supplied pool identifier; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"state": {
		// Provider-computed lifecycle state (ACTIVE / DELETED / etc.).
		// Surface a drift hit if it ever flips to DELETED out-of-band.
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
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
	"disabled": {
		// Global kill-switch for every downstream WIF flow. Silent
		// flips are real security-pillar events. RequiresApproval so
		// the operator confirms against a plan before flipping.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_iam_workload_identity_pool", googleIAMWorkloadIdentityPoolPolicy)
}
