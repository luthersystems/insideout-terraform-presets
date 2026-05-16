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

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_eks_node_group", awsEKSNodeGroupPolicy)
}
