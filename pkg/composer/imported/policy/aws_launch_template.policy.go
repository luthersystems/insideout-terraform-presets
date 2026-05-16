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

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_launch_template", awsLaunchTemplatePolicy)
}
