package policy

// googleCloudRunV2ServicePolicy curates Layer 2 for
// `google_cloud_run_v2_service`. Identity scalars are tagged
// DriftSemanticExact; `custom_audiences` is a list-valued audience
// allowlist where authored order is the meaningful drift signal so it
// uses DriftSemanticWholeList. Other curated fields stay
// DriftSemanticNone until per-leaf comparators land.
var googleCloudRunV2ServicePolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — top-level
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"ingress": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
	},
	"launch_stage": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"invoker_iam_disabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"deletion_protection": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"custom_audiences": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Template — image + scaling + concurrency are the core knobs.
	"template.containers.image": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"template.containers.name": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},
	"template.containers.args": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.containers.command": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.containers.env.name": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.containers.env.value": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
		Sensitivity: SensitivityRedacted,
	},
	"template.containers.resources.limits": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.containers.ports.container_port": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.scaling.min_instance_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"template.scaling.max_instance_count": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"template.max_instance_request_concurrency": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.timeout": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},
	"template.execution_environment": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.vpc_access.connector": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"template.vpc_access.egress": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"template.revision": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},

	// Traffic split between revisions.
	"traffic.type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"traffic.percent": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"traffic.revision": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Binary authorization.
	"binary_authorization.use_default": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"binary_authorization.policy": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Annotations and labels — system-owned.
	"annotations":           tagPolicy(),
	"effective_annotations": tagPolicy(),
	"labels":                tagPolicy(),
	"effective_labels":      tagPolicy(),
	"terraform_labels":      tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_cloud_run_v2_service", googleCloudRunV2ServicePolicy)
}
