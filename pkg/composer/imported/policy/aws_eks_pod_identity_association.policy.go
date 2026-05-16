package policy

// awsEKSPodIdentityAssociationPolicy curates Layer 2 for
// `aws_eks_pod_identity_association`.
//
// Pod identity associations are the post-IRSA way to grant AWS API
// access to EKS pods: bind a (cluster_name, namespace,
// service_account) tuple to an IAM role ARN. Identity is
// (association_id, association_arn). Drift on role_arn silently
// re-grants privileges to every pod backed by the bound service
// account — high-signal security event.
//
// Drift bundle 12 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy().
var awsEKSPodIdentityAssociationPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"association_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"association_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — cluster + Kubernetes service account -------------------
	"cluster_name": {
		// EKS cluster the association lives in. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"namespace": {
		// K8s namespace of the SA.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"service_account": {
		// K8s service-account name. (namespace, service_account) is the
		// trust boundary.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Security-critical — IAM role granted to the bound SA ------------
	"role_arn": {
		// IAM role assumed by every pod backed by the SA. Drift = silent
		// privilege re-grant.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_eks_pod_identity_association", awsEKSPodIdentityAssociationPolicy)
}
