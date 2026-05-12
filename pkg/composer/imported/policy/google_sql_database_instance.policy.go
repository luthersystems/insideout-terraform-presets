package policy

var googleSqlDatabaseInstancePolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"database_version": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
	},

	// Wiring — CMEK + replica source.
	"encryption_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},
	"master_instance_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},

	// Tuning
	"instance_type": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"deletion_protection": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"maintenance_version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Root password is the bootstrap secret — sensitive + redacted.
	"root_password": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityHidden,
		Edit: EditSystemOnly, Sensitivity: SensitivitySensitive,
	},

	// Settings block — the operational surface.
	"settings.tier": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"settings.edition": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"settings.availability_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"settings.disk_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"settings.disk_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
	},
	"settings.disk_autoresize": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.disk_autoresize_limit": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.deletion_protection_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"settings.activation_policy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.pricing_plan": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
	},

	// Backup configuration.
	"settings.backup_configuration.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"settings.backup_configuration.start_time": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.backup_configuration.point_in_time_recovery_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"settings.backup_configuration.transaction_log_retention_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.backup_configuration.backup_retention_settings.retained_backups": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// IP configuration.
	"settings.ip_configuration.ipv4_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
	},
	"settings.ip_configuration.private_network": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"settings.ip_configuration.allocated_ip_range": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"settings.ip_configuration.ssl_mode": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"settings.ip_configuration.authorized_networks.value": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"settings.ip_configuration.authorized_networks.name": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Maintenance window.
	"settings.maintenance_window.day": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.maintenance_window.hour": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.maintenance_window.update_track": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Database flags + insights.
	"settings.database_flags.name": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"settings.database_flags.value": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
	},
	"settings.insights_config.query_insights_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// User labels live under settings.user_labels for SQL.
	"settings.user_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_sql_database_instance", googleSqlDatabaseInstancePolicy)
}
