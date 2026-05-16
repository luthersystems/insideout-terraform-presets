package policy

// awsEKSNodeGroupPolicy curates Layer 2 for `aws_eks_node_group`. Cloud-
// control-routed enrichment already produces typed Attrs; this map adds
// the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An EKS Managed Node Group is a versioned worker-node pool attached to
// an EKS cluster. Identity is (arn, id, node_group_name) and the wiring
// axis is (cluster_name × node_role_arn × subnet_ids). The compute
// surface is (ami_type × capacity_type × instance_types × disk_size).
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact;
// instance_types + subnet_ids are lists, marked DriftSemanticWholeList.
// Nested blocks (scaling_config, update_config, taint, launch_template,
// remote_access) are left uncurated — block-level drift is a follow-up.
// Tags use tagPolicy(). Timeouts use timeoutsPolicy().
//
// Depth-pass extras (#482 follow-up): adds the curated nested blocks
// the original bundle deferred — `scaling_config.*` (autoscaling
// bounds), `update_config.*` (rolling-update guardrails),
// `launch_template.*` (custom LT wiring), `remote_access.*` (SSH
// access), `taint.*` (Kubernetes-level taints), and `resources.*`
// (provider-reported child resource IDs).
var awsEKSNodeGroupPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"node_group_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"node_group_name_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"status": {
		// ACTIVE | CREATING | UPDATING | DELETING — computed.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent cluster + IAM role + subnets --------------------
	"cluster_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"node_role_arn": {
		// IAM role assumed by each kubelet — security-critical.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet_ids": {
		// Subnets where nodes are launched. Drift here flags reachability /
		// AZ-balance changes.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Compute surface --------------------------------------------------
	"ami_type": {
		// AL2_x86_64 | AL2_x86_64_GPU | AL2_ARM_64 | BOTTLEROCKET_* etc.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"capacity_type": {
		// ON_DEMAND | SPOT.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"instance_types": {
		// Mixed-instance set — managed node group picks the cheapest
		// matching capacity at launch.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"disk_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		// Kubernetes minor version (e.g. "1.28"). Out-of-band drift here
		// flags a manual upgrade.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"release_version": {
		// AMI release version pinned to the K8s minor.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"force_update_version": {
		// Pod-eviction-bypass flag during upgrade. Operator-only.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Scaling config ----------------------------------------------------
	"scaling_config.min_size": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"scaling_config.max_size": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"scaling_config.desired_size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Update config (rolling-update guardrails) ------------------------
	"update_config.max_unavailable": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"update_config.max_unavailable_percentage": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Launch template wiring -------------------------------------------
	"launch_template.id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"launch_template.name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"launch_template.version": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Remote (SSH) access ----------------------------------------------
	"remote_access.ec2_ssh_key": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"remote_access.source_security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Kubernetes taints ------------------------------------------------
	"taint.key": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"taint.value": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"taint.effect": {
		// NO_SCHEDULE | NO_EXECUTE | PREFER_NO_SCHEDULE.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Provider-reported child resource IDs -----------------------------
	"resources.autoscaling_groups": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},
	"resources.remote_access_security_group_id": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_eks_node_group", awsEKSNodeGroupPolicy)
}
