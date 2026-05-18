package policy

// googleDNSManagedZonePolicy curates Layer 2 for
// `google_dns_managed_zone`. Zone identity is (project, name, dns_name)
// with visibility (public / private) pinned at create. DNSSEC config is
// the security-critical surface — silent flips between off and on, or
// changes to non_existence (NSEC vs NSEC3), are out-of-band cryptographic
// changes that must surface as drift.
//
// Private-visibility associations carry network bindings — the
// `private_visibility_config.networks` and `gke_clusters` lists are
// curated whole-list so a missing-or-extra association is one diff
// entry.
//
// Bundle (#599): part of the DNS+cert mega-bundle. Codegen-only — see
// gcpCodegenOnlyTypes in pkg/insideout-import/registry/registry.go for
// the rationale on shipping drift policy before the CAI discoverer
// hookup. Labels use gcpLabelDriftPolicy() so user-set labels (like
// the canonical `project = <project>` label) participate in drift.
var googleDNSManagedZonePolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"managed_zone_id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Zone resource name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"dns_name": {
		// FQDN apex (trailing dot). Pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"visibility": {
		// "public" / "private". Pinned at create; switching breaks the zone.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"creation_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name_servers": {
		// Cloud DNS-assigned NS set for the zone. Whole-list compare so
		// an unexpected NS change is one diff entry.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning ------------------------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"force_destroy": {
		// Destructive flag; system-owned.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditSystemOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// DNSSEC config — security-critical -------------------------------
	"dnssec_config.kind": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"dnssec_config.state": {
		// "off" / "on" / "transfer". A silent flip is a real security
		// posture change.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"dnssec_config.non_existence": {
		// "nsec" / "nsec3"; pinned at DNSSEC enable.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"dnssec_config.default_key_specs.algorithm": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"dnssec_config.default_key_specs.key_length": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"dnssec_config.default_key_specs.key_type": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"dnssec_config.default_key_specs.kind": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Cloud Logging — informational toggle ----------------------------
	"cloud_logging_config.enable_logging": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Forwarding config — DNS forwarding for private zones ------------
	"forwarding_config.target_name_servers.forwarding_path": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"forwarding_config.target_name_servers.ipv4_address": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Peering config ----------------------------------------------------
	"peering_config.target_network.network_url": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Private visibility config — VPC + GKE associations -------------
	"private_visibility_config.networks.network_url": {
		// Per-network association; cross-resource wiring.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"private_visibility_config.gke_clusters.gke_cluster_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels ------------------------------------------------------------
	"labels":           gcpLabelDriftPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_dns_managed_zone", googleDNSManagedZonePolicy)
}
