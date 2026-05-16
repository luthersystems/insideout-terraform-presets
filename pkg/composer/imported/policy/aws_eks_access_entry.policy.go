package policy

// awsEKSAccessEntryPolicy curates Layer 2 for `aws_eks_access_entry`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An EKS access entry binds an IAM principal (role/user) onto a cluster's
// RBAC. Identity is (cluster_name, principal_arn, access_entry_arn). The
// (type, kubernetes_groups, user_name) tuple drives what permissions the
// principal carries inside the cluster — out-of-band changes silently
// re-key cluster authorization.
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact; the
// kubernetes_groups slice uses DriftSemanticWholeList (order-insensitive
// is the right semantic but WholeList is the supported "any change is
// drift" knob). Tags use tagPolicy().
var awsEKSAccessEntryPolicy = Map{
	// Identity ----------------------------------------------------------
	"access_entry_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent cluster + IAM principal -------------------------
	"cluster_name": {
		// Pointer to the parent aws_eks_cluster. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"principal_arn": {
		// IAM principal (role/user) the entry authorizes. Pinned at create.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — RBAC mapping -------------------------------------------
	"type": {
		// STANDARD / EC2_LINUX / EC2_WINDOWS / FARGATE_LINUX — drives
		// which AWS-managed groups the principal inherits.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"kubernetes_groups": {
		// In-cluster RBAC groups the principal joins. Security-critical
		// — drift here changes effective permissions.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"user_name": {
		// In-cluster username override; affects RBAC audit attribution.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_eks_access_entry", awsEKSAccessEntryPolicy)
}
