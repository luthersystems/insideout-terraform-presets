package policy

// googleContainerClusterPolicy curates Layer 2 for
// `google_container_cluster`. Identity scalars and the VPC wiring
// leaves are tagged DriftSemanticExact so drift detection surfaces
// cluster relocation / re-parenting / VPC re-pointing. The list-valued
// `node_config.oauth_scopes` uses DriftSemanticWholeList — the
// authored set of scopes is the meaningful drift signal regardless
// of element order. Other curated fields stay DriftSemanticNone until
// per-leaf comparators land.
var googleContainerClusterPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — VPC.
	"network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"subnetwork": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — top-level cluster knobs.
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"min_master_version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"node_version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_autopilot": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_shielded_nodes": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_intranode_visibility": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_legacy_abac": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"deletion_protection": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"networking_mode": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"datapath_provider": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"initial_node_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"remove_default_node_pool": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"default_max_pods_per_node": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	// Node-pool defaults.
	"node_config.machine_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.disk_size_gb": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.disk_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.oauth_scopes": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	// IP allocation policy (VPC-native).
	"ip_allocation_policy.cluster_secondary_range_name": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"ip_allocation_policy.services_secondary_range_name": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"ip_allocation_policy.cluster_ipv4_cidr_block": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"ip_allocation_policy.services_ipv4_cidr_block": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	// Private cluster.
	"private_cluster_config.enable_private_nodes": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"private_cluster_config.enable_private_endpoint": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"private_cluster_config.master_ipv4_cidr_block": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Master auth.
	"master_auth.client_certificate_config.issue_client_certificate": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	// Encryption at rest (etcd).
	"database_encryption.state": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"database_encryption.key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Workload identity.
	"workload_identity_config.workload_pool": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Release channel.
	"release_channel.channel": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels — system-owned. resource_labels is the GKE-canonical name;
	// the *_labels family follows the standard pattern.
	"resource_labels":  tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_container_cluster", googleContainerClusterPolicy)
}
