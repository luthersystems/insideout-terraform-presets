package policy

// awsEKSFargateProfilePolicy curates Layer 2 for `aws_eks_fargate_profile`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An EKS Fargate profile maps a namespace/label selector onto Fargate
// compute. Identity is (cluster_name, fargate_profile_name, arn, id).
// pod_execution_role_arn is the IRSA role that the Fargate scheduler
// assumes; subnet_ids constrain where Fargate pods land. The nested
// `selector` block (uncurated here as a block — modeled by its
// containment) drives which pods route to Fargate vs. EC2.
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact; subnet_ids
// uses DriftSemanticWholeList. Tags use tagPolicy(). The `timeouts`
// nested block is left uncurated — provider-process plumbing.
var awsEKSFargateProfilePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"fargate_profile_name": {
		// Profile name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent cluster + execution role + subnets --------------
	"cluster_name": {
		// Pointer to the parent aws_eks_cluster. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"pod_execution_role_arn": {
		// IRSA role the Fargate scheduler assumes for pods. Security
		// boundary — RequiresApproval.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet_ids": {
		// Set of subnets Fargate uses for ENIs. Retargeting changes the
		// network blast radius — RequiresApproval.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning — observable status --------------------------------------
	"status": {
		// CREATING / ACTIVE / DELETING / etc.; provider-reported lifecycle.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_eks_fargate_profile", awsEKSFargateProfilePolicy)
}
