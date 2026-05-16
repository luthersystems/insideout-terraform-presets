package policy

// awsAcmCertificatePolicy curates Layer 2 for `aws_acm_certificate`.
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// ACM certificate identity is the ARN. `domain_name` and
// `subject_alternative_names` are pinned at create (provider forces
// replacement on change). `validation_method` and `key_algorithm` are
// security-posture knobs — TLS-cert mis-configuration is a posture
// regression that drift detection must surface.
//
// Drift bundle 3 (#482): scalar attributes use DriftSemanticExact.
// `subject_alternative_names` is order-insensitive in practice but the
// provider stores it as an ordered list — WholeList compare reports a
// missing/extra SAN as one diff entry.
//
// Depth-pass extras (#482 follow-up): adds the imported-cert input
// scalars (`certificate_body`, `certificate_chain`, `private_key` —
// the last is Sensitive so it stays Hidden/SystemOnly with
// DriftSemanticNone so the comparator never echoes the key), the
// `domain_validation_options.*` server-emitted DNS challenge tuples,
// the `renewal_summary.*` ACM renewal status, the
// `validation_option.*` per-SAN override block, and
// `validation_emails`.
var awsAcmCertificatePolicy = Map{
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
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"status": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"type": {
		// AMAZON_ISSUED vs IMPORTED vs PRIVATE — pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"not_after": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"not_before": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — cross-resource references --------------------------------
	"certificate_authority_arn": {
		// Private CA ARN for AWS_PRIVATE_CA-issued certs.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — TLS posture ---------------------------------------------
	"validation_method": {
		// DNS / EMAIL / NONE. Pinned at create; replacement on change.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"key_algorithm": {
		// RSA_2048 / EC_prime256v1 / EC_secp384r1 etc. Pinned at create.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"early_renewal_duration": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"renewal_eligibility": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"pending_renewal": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Lists --------------------------------------------------------------
	"subject_alternative_names": {
		// Order-insensitive SAN set; whole-list compare so a missing or
		// extra SAN is one diff entry, not N.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Options block — transparency logging -----------------------------
	"options.certificate_transparency_logging_preference": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Imported-cert payload (IMPORTED type) ----------------------------
	"certificate_body": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"certificate_chain": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"private_key": {
		// Sensitive — comparator must never emit the value.
		Role: RoleTuning, Pillar: PillarSecurity,
		Visibility:  VisibilityHidden,
		Edit:        EditSystemOnly,
		Sensitivity: SensitivitySensitive,
	},

	// Server-emitted DNS challenge tuples ------------------------------
	"domain_validation_options.domain_name": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_validation_options.resource_record_name": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_validation_options.resource_record_type": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_validation_options.resource_record_value": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Renewal status ---------------------------------------------------
	"renewal_summary.renewal_status": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"renewal_summary.renewal_status_reason": {
		Role: RoleIdentity, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"renewal_summary.updated_at": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Per-SAN validation override -------------------------------------
	"validation_option.domain_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"validation_option.validation_domain": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	"validation_emails": {
		// Server-emitted list of contacts for EMAIL validation.
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_acm_certificate", awsAcmCertificatePolicy)
}
