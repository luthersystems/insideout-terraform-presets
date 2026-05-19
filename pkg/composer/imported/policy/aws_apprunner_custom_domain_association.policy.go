package policy

// awsApprunnerCustomDomainAssociationPolicy curates Layer 2 for
// `aws_apprunner_custom_domain_association`.
//
// #623 backfill for the aws/apprunner preset (#598 / #620). A custom
// domain association binds a caller-owned domain name to an App Runner
// service. AWS validates the cert asynchronously via DNS-01 records;
// `certificate_validation_records` is a provider-computed list of
// objects holding the per-record name/value/type the caller must add
// in their DNS provider — curated with `DriftSemanticWholeList` so a
// record-set re-issuance (different name/value) surfaces as drift.
//
// NOTE: this resource type does NOT accept tags. Confirmed in the
// repo's NON_TAGGABLE_AWS allowlist (tests/lint-project-tag.sh) and
// the untaggableAWS map in pkg/composer/imported_provenance.go. The
// `tags` / `tags_all` entries are intentionally omitted.
var awsApprunnerCustomDomainAssociationPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"dns_target": {
		// The AppRunner-owned CNAME target the caller's DNS must point at.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"status": {
		// pending_certificate_dns_validation → active → ...
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	// certificate_validation_records is the read-only DNS-01 record set
	// the caller must add in their DNS provider to complete the cert
	// handshake. Re-issuance re-shapes the list, so WholeList is the
	// meaningful drift granularity.
	"certificate_validation_records": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Wiring — the bound service --------------------------------------
	"service_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning ---------------------------------------------------------
	"enable_www_subdomain": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_apprunner_custom_domain_association", awsApprunnerCustomDomainAssociationPolicy)
}
