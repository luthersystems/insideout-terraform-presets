package policy

// googleComputeManagedSslCertificatePolicy curates Layer 2 for
// `google_compute_managed_ssl_certificate`. Identity + spec scalars use
// DriftSemanticExact; the list-valued `subject_alternative_names` and
// `managed.domains` use DriftSemanticWholeList — the authored SAN /
// domain set is the meaningful drift signal regardless of order.
var googleComputeManagedSslCertificatePolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"certificate_id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"subject_alternative_names": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},
	"expire_time": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditNever,
	},
	"creation_timestamp": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},

	// Managed block — domains are immutable post-create.
	"managed.domains": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_managed_ssl_certificate", googleComputeManagedSslCertificatePolicy)
}
