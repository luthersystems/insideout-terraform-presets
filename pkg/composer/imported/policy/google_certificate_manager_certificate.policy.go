package policy

// googleCertificateManagerCertificatePolicy curates Layer 2 for
// `google_certificate_manager_certificate`. Identity is (project,
// location, name); `scope` (DEFAULT / EDGE_CACHE / ALL_REGIONS) is
// pinned at create. The `managed` sub-block carries domains +
// DNS-authorizations for Google-issued certs; the `self_managed`
// sub-block holds PEM material for customer-supplied certs.
//
// Security-critical surface:
//   - managed.domains and managed.dns_authorizations control what
//     hostnames the issued cert covers — whole-list compare.
//   - managed.issuance_config wires in a private-PKI issuance config.
//   - self_managed.pem_certificate is a public-PEM body change requiring
//     approval; self_managed.pem_private_key is Sensitive and stays
//     Hidden/SystemOnly with DriftSemantic=None so the comparator never
//     echoes the key.
//
// Bundle (#599): part of the DNS+cert mega-bundle. Codegen-only — see
// gcpCodegenOnlyTypes. Mirrors the existing
// google_compute_managed_ssl_certificate policy where applicable.
var googleCertificateManagerCertificatePolicy = Map{
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
	"location": {
		// Region or "global". Pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"scope": {
		// DEFAULT / EDGE_CACHE / ALL_REGIONS / CLIENT_AUTH. Pinned at
		// create — switching scopes recreates the certificate.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"san_dnsnames": {
		// Server-emitted SAN list. Whole-list compare.
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning — description --------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Managed certificate sub-block ----------------------------------
	"managed.domains": {
		// Domains the managed cert is provisioned for. Pinned at create;
		// whole-list compare.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"managed.dns_authorizations": {
		// References to DNS-authorization resources used to prove domain
		// ownership. Whole-list compare.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"managed.issuance_config": {
		// Private-PKI issuance config reference (mutually exclusive with
		// dns_authorizations).
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"managed.state": {
		// Server-emitted provisioning state.
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"managed.authorization_attempt_info.domain": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"managed.authorization_attempt_info.state": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"managed.authorization_attempt_info.failure_reason": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"managed.authorization_attempt_info.details": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"managed.provisioning_issue.reason": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"managed.provisioning_issue.details": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Self-managed certificate sub-block -----------------------------
	"self_managed.pem_certificate": {
		// Public PEM cert body. Drift on the cert payload is a real
		// security event — RequiresApproval.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"self_managed.certificate_pem": {
		// Legacy alias of pem_certificate; same treatment.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"self_managed.pem_private_key": {
		// Sensitive private key. Comparator must never emit the value;
		// DriftSemantic stays None (the empty-string default).
		Role: RoleTuning, Pillar: PillarSecurity,
		Visibility:  VisibilityHidden,
		Edit:        EditSystemOnly,
		Sensitivity: SensitivitySensitive,
	},
	"self_managed.private_key_pem": {
		// Legacy alias of pem_private_key; same treatment.
		Role: RoleTuning, Pillar: PillarSecurity,
		Visibility:  VisibilityHidden,
		Edit:        EditSystemOnly,
		Sensitivity: SensitivitySensitive,
	},

	// Labels ------------------------------------------------------------
	"labels":           gcpLabelDriftPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_certificate_manager_certificate", googleCertificateManagerCertificatePolicy)
}
