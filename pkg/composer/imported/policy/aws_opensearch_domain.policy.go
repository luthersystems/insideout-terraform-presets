package policy

// awsOpensearchDomainPolicy curates Layer 2 for `aws_opensearch_domain`
// (covers OpenSearch + the legacy Elasticsearch-renamed shape).
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// Domain identity: `domain_name` is the primary key (immutable);
// `engine_version` + `cluster_config.instance_type` + `cluster_config.instance_count`
// define the cluster shape. VPC attachment is `vpc_options`.
//
// Drift bundle (#482): scalar attributes use DriftSemanticExact.
// `vpc_options.subnet_ids` and `.security_group_ids` are
// order-insensitive sets so WholeList compare. Tags use tagPolicy().
//
// Depth-pass extras (#482 follow-up): adds the dual-stack endpoint
// scalars (`endpoint_v2`, `dashboard_endpoint_v2`,
// `domain_endpoint_v2_hosted_zone_id`), warm + cold storage tier
// toggles (`cluster_config.warm_enabled` + count + type, and
// `cluster_config.cold_storage_options.enabled`), `auto_tune_options.*`
// (machine-learned tuning maintenance window), `cognito_options.*`
// (dashboard SSO via Cognito), custom-endpoint TLS surface,
// `log_publishing_options.*` (per-log-type CW destinations),
// `off_peak_window_options`, and `software_update_options.auto_software_update_enabled`.
var awsOpensearchDomainPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"endpoint": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"dashboard_endpoint": {
		// AWS provider 6.x dropped the legacy `kibana_endpoint` alias
		// (deprecated since the Kibana → OpenSearch Dashboards rename);
		// dashboard_endpoint is the canonical surface (#599 schema-bump
		// cleanup).
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"endpoint_v2": {
		// Dual-stack endpoint (IPv4 + IPv6).
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"dashboard_endpoint_v2": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_endpoint_v2_hosted_zone_id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Engine version + IP family --------------------------------------
	"engine_version": {
		// In-place upgradable.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ip_address_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Cluster config — sizing -----------------------------------------
	"cluster_config.instance_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.instance_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.dedicated_master_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.dedicated_master_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.dedicated_master_count": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.zone_awareness_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.multi_az_with_standby_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.warm_enabled": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.warm_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.warm_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cluster_config.cold_storage_options.enabled": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// EBS storage -------------------------------------------------------
	"ebs_options.ebs_enabled": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"ebs_options.volume_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ebs_options.volume_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ebs_options.iops": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ebs_options.throughput": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Encryption (at rest + in transit) -------------------------------
	"encrypt_at_rest.enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"encrypt_at_rest.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"node_to_node_encryption.enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_endpoint_options.enforce_https": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_endpoint_options.tls_security_policy": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_endpoint_options.custom_endpoint_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_endpoint_options.custom_endpoint": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_endpoint_options.custom_endpoint_certificate_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// VPC options + access policies -----------------------------------
	"vpc_options.subnet_ids": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_options.security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"access_policies": {
		// IAM access policy JSON. Security-critical.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"advanced_security_options.enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"advanced_security_options.anonymous_auth_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"advanced_security_options.internal_user_database_enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Snapshots ---------------------------------------------------------
	"snapshot_options.automated_snapshot_start_hour": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Cognito SSO for dashboard ----------------------------------------
	"cognito_options.enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cognito_options.identity_pool_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"cognito_options.user_pool_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"cognito_options.role_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Auto-tune (ML maintenance window) --------------------------------
	"auto_tune_options.desired_state": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"auto_tune_options.rollback_on_disable": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Log publishing (per-log-type CW destinations) --------------------
	"log_publishing_options.log_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"log_publishing_options.cloudwatch_log_group_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"log_publishing_options.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Off-peak maintenance window --------------------------------------
	"off_peak_window_options.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"off_peak_window_options.off_peak_window.window_start_time.hours": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"off_peak_window_options.off_peak_window.window_start_time.minutes": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Software update --------------------------------------------------
	"software_update_options.auto_software_update_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ------------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_opensearch_domain", awsOpensearchDomainPolicy)
}
