package policy

// awsAcmCertificateValidationPolicy curates Layer 2 for
// `aws_acm_certificate_validation`. This is a "wait" resource — it
// blocks until the ACM service marks the certificate ISSUED, and has
// no own-lifecycle state beyond the cert ARN it wraps. The interactive
// drift surface is therefore tiny: certificate_arn is the cross-resource
// wiring (Exact), validation_record_fqdns is the DNS challenge list the
// wait depends on (WholeList), and timeouts is system-owned.
//
// Bundle (#599): added alongside aws_route53_record to close the new
// aws/acm preset's drift coverage gap. Codegen-only — the live
// awsdiscover constructor doesn't list validation resources (they're
// inferred from the parent certificate's domain_validation_options).
var awsAcmCertificateValidationPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		// AWS region the validation runs against; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — the certificate being validated -------------------------
	"certificate_arn": {
		// ARN of the aws_acm_certificate this resource blocks on. Pinned
		// at create (the wait is per-cert). Security pillar — the
		// validation is the gate that flips the cert from PENDING_VALIDATION
		// to ISSUED.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — DNS challenge FQDN list ---------------------------------
	"validation_record_fqdns": {
		// Caller-supplied list of FQDN challenge records (typically
		// derived from the parent cert's domain_validation_options +
		// route53 record set). Whole-list compare so a missing-or-extra
		// FQDN is one diff entry.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_acm_certificate_validation", awsAcmCertificateValidationPolicy)
}
