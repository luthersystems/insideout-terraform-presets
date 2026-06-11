package policy

// awsSagemakerModelPolicy curates Layer 2 for `aws_sagemaker_model`.
//
// #787 backfill for the SageMaker real-time inference triad (#761):
// model → endpoint configuration → endpoint. A SageMaker model binds an
// inference container image to the IAM role the endpoint assumes at
// runtime. The two load-bearing drift surfaces are:
//
//   - execution_role_arn — the identity every hosted inference container
//     runs as. A silent rebind re-purposes the model's IAM identity
//     (privilege-escalation shaped).
//   - the container image + model_data_url — what code/weights the
//     endpoint actually serves. An out-of-band image tag bump silently
//     ships a different model.
//
// network isolation and the VPC wiring are the security-pillar
// boundary controls. The model is immutable in AWS (every field is
// replace-on-change at the resource level), so curation focuses on the
// fields whose drift is a meaningful security/reliability signal rather
// than enumerating the full container config tree.
var awsSagemakerModelPolicy = Map{
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
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — runtime IAM identity ----------------------------------
	"execution_role_arn": {
		// Silent rebind re-purposes every hosted container's IAM identity.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Network isolation boundary -------------------------------------
	"enable_network_isolation": {
		// Flipping to false lets the inference container reach the network.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Primary container — what the endpoint serves -------------------
	"primary_container.image": {
		// ECR image URI — an out-of-band tag bump ships different code.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"primary_container.model_data_url": {
		// S3 URI of the model weights/artifact.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"primary_container.mode": {
		// SingleModel | MultiModel — affects serving semantics.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// VPC wiring — security boundary ---------------------------------
	"vpc_config.security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_config.subnets": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_sagemaker_model", awsSagemakerModelPolicy)
}
