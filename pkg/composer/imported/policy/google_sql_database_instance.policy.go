package policy

// googleSqlDatabaseInstancePolicy curates Layer 2 for
// `google_sql_database_instance`.
//
// Bundle G5 (#482): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Scalars use DriftSemanticExact. The
// `settings.ip_configuration.authorized_networks.*` paths are nested
// list-of-block elements; their inner scalars use DriftSemanticExact
// because each authorized-network row is independently meaningful
// (drift on a specific CIDR is the actionable signal). `user_labels`
// stays at the tagPolicy() default (DriftSemanticNone); LabelFilter
// coverage on user-author labels is deferred until the drift
// comparator's redacted-mode output is in place.
//
// root_password — Sensitivity=Sensitive. The API doesn't return it on
// read; comparator never sees it. DriftSemantic intentionally unset
// (DriftSemanticNone).
var googleSqlDatabaseInstancePolicy = Map{
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
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"database_version": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — CMEK + replica source.
	"encryption_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"master_instance_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"instance_type": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"deletion_protection": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"maintenance_version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Root password is the bootstrap secret — sensitive + redacted.
	// DriftSemantic deferred (Sensitivity=Sensitive; comparator never
	// receives the value).
	"root_password": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityHidden,
		Edit: EditSystemOnly, Sensitivity: SensitivitySensitive,
	},

	// Settings block — the operational surface.
	"settings.tier": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.edition": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.availability_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.disk_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.disk_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.disk_autoresize": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.disk_autoresize_limit": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.deletion_protection_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.activation_policy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.pricing_plan": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Backup configuration.
	"settings.backup_configuration.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.backup_configuration.start_time": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.backup_configuration.point_in_time_recovery_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.backup_configuration.transaction_log_retention_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.backup_configuration.backup_retention_settings.retained_backups": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// IP configuration.
	"settings.ip_configuration.ipv4_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.ip_configuration.private_network": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.ip_configuration.allocated_ip_range": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.ip_configuration.ssl_mode": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.ip_configuration.authorized_networks.value": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.ip_configuration.authorized_networks.name": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Maintenance window.
	"settings.maintenance_window.day": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.maintenance_window.hour": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.maintenance_window.update_track": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Database flags + insights.
	"settings.database_flags.name": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.database_flags.value": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"settings.insights_config.query_insights_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// User labels live under settings.user_labels for SQL.
	"settings.user_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_sql_database_instance", googleSqlDatabaseInstancePolicy)
}
