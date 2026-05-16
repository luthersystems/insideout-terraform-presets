package policy

// awsEKSAddonPolicy curates Layer 2 for `aws_eks_addon`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An EKS managed add-on is an AWS-managed cluster-component install
// (vpc-cni, coredns, kube-proxy, ebs-csi, etc.). Identity is
// (cluster_name, addon_name, id, arn). `addon_version` pins which
// upstream version lands in the cluster; the `resolve_conflicts_on_*`
// knobs gate how the API reconciles operator overrides during
// install/update. `configuration_values` is an optional JSON blob the
// AWS API merges into the add-on's helm/manifest values. Out-of-band
// version flips are the highest-signal regression.
//
// Drift bundle 10 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy(). The `timeouts` nested block is left uncurated — it's
// provider-process plumbing, not server state.
var awsEKSAddonPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"addon_name": {
		// Upstream add-on identifier (vpc-cni, coredns, …); pinned at
		// create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent cluster + service role --------------------------
	"cluster_name": {
		// Pointer to the parent aws_eks_cluster. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"service_account_role_arn": {
		// IRSA role the add-on's pods assume. Retargeting changes
		// in-cluster identity — RequiresApproval.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — version + reconciliation knobs -------------------------
	"addon_version": {
		// Pinned upstream version (e.g. v1.18.1-eksbuild.1). Drift
		// indicates an out-of-band upgrade-through-AWS-console.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"configuration_values": {
		// JSON blob merged into the add-on values. Exact-string drift
		// catches out-of-band reconfig.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"resolve_conflicts_on_create": {
		// NONE / OVERWRITE; how the AWS API reconciles operator overrides
		// at install time.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"resolve_conflicts_on_update": {
		// NONE / OVERWRITE / PRESERVE; reconciliation policy on update.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_eks_addon", awsEKSAddonPolicy)
}
