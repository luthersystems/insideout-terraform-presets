package policy

// awsLaunchTemplatePolicy curates Layer 2 for `aws_launch_template`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Launch Template is a versioned config used by Auto Scaling Groups
// and EC2 Fleet to launch instances: AMI, instance type, network /
// security-group set, user_data, and a long list of nested
// configuration blocks. Identity is (arn, id, name). `default_version`
// / `latest_version` are computed and increment on each new version.
//
// Drift bundle 7 (#482): scalars use DriftSemanticExact;
// security_group_names and vpc_security_group_ids are lists, marked
// DriftSemanticWholeList. Nested blocks (block_device_mappings,
// network_interfaces, metadata_options, etc.) are left uncurated —
// block-level drift is a follow-up. Top-level scalar drift covers the
// highest-signal axes (AMI changed, instance type changed, user_data
// hash changed, SG set changed).
//
// Depth-pass extras (#482 follow-up): adds the highest-signal nested
// blocks the original bundle deferred — `iam_instance_profile.arn|name`,
// `metadata_options.*` (IMDSv1/v2 toggle is security-critical),
// `monitoring.enabled` (detailed CloudWatch), `placement.*` (AZ /
// affinity / tenancy), `hibernation_options.configured`,
// `enclave_options.enabled` (Nitro Enclaves — security-critical),
// `cpu_options.*` (per-instance core/thread count), `credit_specification.cpu_credits`,
// the `capacity_reservation_specification.*` wiring, and
// `private_dns_name_options.hostname_type`.
var awsLaunchTemplatePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"name_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"default_version": {
		// Monotonically incrementing per version edit.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"latest_version": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — AMI / instance type / network ---------------------------
	"image_id": {
		// AMI ID. Cross-resource pointer; drift here usually flags a
		// silent AMI rotation.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"instance_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"key_name": {
		// SSH key pair for shell access.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc_security_group_ids": {
		// VPC SG set applied to launched instances.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"security_group_names": {
		// Classic-EC2 SG names — legacy, rarely used in modern VPC setups.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// User data + boot behavior ----------------------------------------
	"user_data": {
		// Base64-encoded boot script. Operator-controlled.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ebs_optimized": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"disable_api_stop": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"disable_api_termination": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"instance_initiated_shutdown_behavior": {
		// stop | terminate.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"update_default_version": {
		// Provider-side flag — bump default_version on every TF apply.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"kernel_id": {
		// Para-virtual kernel ID — legacy; PV instances are deprecated.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ram_disk_id": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// IAM instance profile wiring --------------------------------------
	"iam_instance_profile.arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"iam_instance_profile.name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Metadata options (IMDSv1/v2 toggle is security-critical) ---------
	"metadata_options.http_endpoint": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"metadata_options.http_tokens": {
		// required (IMDSv2-only) | optional.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"metadata_options.http_put_response_hop_limit": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"metadata_options.http_protocol_ipv6": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"metadata_options.instance_metadata_tags": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Detailed monitoring -----------------------------------------------
	"monitoring.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Placement (AZ / affinity / tenancy) ------------------------------
	"placement.availability_zone": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"placement.tenancy": {
		// default | dedicated | host.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"placement.group_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"placement.host_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"placement.host_resource_group_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Hibernation / Enclave / CPU tuning -------------------------------
	"hibernation_options.configured": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"enclave_options.enabled": {
		// Nitro Enclaves — security-critical capability.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cpu_options.core_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cpu_options.threads_per_core": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cpu_options.amd_sev_snp": {
		// Confidential-computing toggle.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"credit_specification.cpu_credits": {
		// standard | unlimited for burstable T-family.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Capacity reservation wiring --------------------------------------
	"capacity_reservation_specification.capacity_reservation_preference": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"capacity_reservation_specification.capacity_reservation_target.capacity_reservation_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Private DNS hostname behavior ------------------------------------
	"private_dns_name_options.hostname_type": {
		// ip-name | resource-name.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"private_dns_name_options.enable_resource_name_dns_a_record": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"private_dns_name_options.enable_resource_name_dns_aaaa_record": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Maintenance options ----------------------------------------------
	"maintenance_options.auto_recovery": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_launch_template", awsLaunchTemplatePolicy)
}
