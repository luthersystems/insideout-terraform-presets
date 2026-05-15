package policy

var googleComputeManagedSslCertificatePolicy = Map{
	// Identity
	"name":           {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":             {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link":      {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"certificate_id": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"subject_alternative_names": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditNever,
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
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_managed_ssl_certificate", googleComputeManagedSslCertificatePolicy)
}
