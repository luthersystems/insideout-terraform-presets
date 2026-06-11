package policy

// awsSagemakerEndpointConfigurationPolicy curates Layer 2 for
// `aws_sagemaker_endpoint_configuration`.
//
// #787 backfill for the SageMaker real-time inference triad (#761). An
// endpoint configuration is the immutable spec an endpoint deploys: it
// binds one or more production_variants (each pinning a model_name +
// instance sizing + traffic weight) and the KMS key for the inference
// volume. The high-value drift surfaces are:
//
//   - production_variants.model_name — which model the endpoint serves.
//     A silent rebind swaps the served model out from under the
//     endpoint.
//   - production_variants.instance_type / initial_instance_count —
//     capacity + cost; silent edits are reliability/cost drift.
//   - kms_key_arn + data_capture_config.kms_key_id — at-rest protection
//     of the inference volume and any captured request/response payloads.
//
// The resource is immutable (replace-on-change), so curation targets the
// drift-meaningful fields rather than the full async/shadow/serverless
// config trees.
var awsSagemakerEndpointConfigurationPolicy = Map{
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

	// Wiring — runtime IAM + at-rest KMS -----------------------------
	"execution_role_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Production variants — what is served + at what scale -----------
	"production_variants.variant_name": {
		// The stable variant identity that traffic weights / data-capture
		// key off of. For multi-variant configs it disambiguates which
		// model+capacity owns which traffic share.
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"production_variants.model_name": {
		// Silent rebind swaps the served model out from under the endpoint.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"production_variants.instance_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"production_variants.initial_instance_count": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"production_variants.initial_variant_weight": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Data capture — payload logging at-rest protection --------------
	"data_capture_config.enable_capture": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"data_capture_config.kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_sagemaker_endpoint_configuration", awsSagemakerEndpointConfigurationPolicy)
}
