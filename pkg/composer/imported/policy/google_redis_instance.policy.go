package policy

var googleRedisInstancePolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"display_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},

	// Wiring — VPC + CMEK.
	"authorized_network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},
	"customer_managed_key": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},
	"reserved_ip_range": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"secondary_ip_range": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever,
	},

	// Tuning — sizing + tier + version.
	"tier": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"memory_size_gb": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"redis_version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"replica_count": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"read_replicas_mode": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"connect_mode": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"location_id": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"alternative_location_id": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},

	// Security knobs.
	"auth_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"transit_encryption_mode": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	// auth_string is the generated AUTH token — sensitive bootstrap secret.
	"auth_string": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityHidden,
		Edit: EditSystemOnly, Sensitivity: SensitivitySensitive,
	},

	// Operational knobs.
	"redis_configs":           tagPolicy(),
	"maintenance_version":     {Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe},
	"maintenance_policy.weekly_maintenance_window.day": {Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe},
	"persistence_config.persistence_mode": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"persistence_config.rdb_snapshot_period": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_redis_instance", googleRedisInstancePolicy)
}
