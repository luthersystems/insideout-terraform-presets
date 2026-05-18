package policy

// googleCertificateManagerCertificateMapEntryPolicy curates Layer 2 for
// `google_certificate_manager_certificate_map_entry`. A map entry binds
// one or more certificates into a parent certificate_map, keyed either
// by `hostname` (typical SNI flow) or by `matcher` (catch-all). Exactly
// one of those two must be set at create — both are AlwaysReplace
// identity scalars.
//
// The `certificates` list is the security-critical surface — silently
// re-pointing a hostname at a different cert is a real TLS-posture
// change, so it's curated WholeList with EditRequiresApproval.
//
// Bundle (#599): part of the DNS+cert mega-bundle. Codegen-only — see
// gcpCodegenOnlyTypes.
var googleCertificateManagerCertificateMapEntryPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"map": {
		// Parent certificate_map this entry belongs to. Cross-resource
		// wiring; pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"hostname": {
		// SNI hostname this entry serves (mutually exclusive with matcher).
		// Pinned at create; identity-shaped.
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"matcher": {
		// Catch-all matcher ("PRIMARY"). Mutually exclusive with hostname.
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"state": {
		// Server-emitted provisioning state.
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"create_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"update_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Security-critical: which certs this entry mounts ---------------
	"certificates": {
		// References to google_certificate_manager_certificate rows. A
		// silent change here silently re-points the SNI hostname at a
		// different TLS cert — real security event. Whole-list compare.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning ------------------------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels ------------------------------------------------------------
	"labels":           gcpLabelDriftPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_certificate_manager_certificate_map_entry", googleCertificateManagerCertificateMapEntryPolicy)
}
