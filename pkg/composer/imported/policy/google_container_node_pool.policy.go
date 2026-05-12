package policy

var googleContainerNodePoolPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"name_prefix": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Wiring — parent cluster.
	"cluster": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning — size + version.
	"initial_node_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"max_pods_per_node": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"node_locations": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// Autoscaling.
	"autoscaling.min_node_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"autoscaling.max_node_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"autoscaling.total_min_node_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"autoscaling.total_max_node_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"autoscaling.location_policy": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Management.
	"management.auto_upgrade": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"management.auto_repair": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},

	// Upgrade settings.
	"upgrade_settings.max_surge": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"upgrade_settings.max_unavailable": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"upgrade_settings.strategy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Node config — machine + disk knobs (mirrors cluster.node_config).
	"node_config.machine_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.disk_size_gb": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.disk_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.preemptible": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"node_config.spot": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_container_node_pool", googleContainerNodePoolPolicy)
}
