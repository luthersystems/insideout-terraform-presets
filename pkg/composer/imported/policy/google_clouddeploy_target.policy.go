package policy

// googleClouddeployTargetPolicy curates Layer 2 for
// `google_clouddeploy_target`.
//
// #623 backfill for the gcp/cloud_deploy preset (#613 / #614). A target
// is a deployment destination — a Cloud Run region or a GKE / Anthos /
// custom-target runtime. The runtime block (`run`/`gke`/`anthos_cluster`
// /`multi_target`/`custom_target`) carries the actual cross-resource
// anchor; the `execution_configs.service_account` field pins the
// identity Cloud Deploy assumes when running render/deploy/verify jobs
// — a silent rebind would be a privilege-escalation attack vector,
// hence `DriftSemanticExact` + Security pillar.
var googleClouddeployTargetPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"target_id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"uid": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"etag": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
	},
	"create_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"update_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — project / location anchors -----------------------------
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

	// Tuning ----------------------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"require_approval": {
		// Approval gating is the only manual checkpoint between a release
		// rollout and a production deploy; silently flipping this to false
		// removes the human-in-the-loop.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"deploy_parameters": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
	},

	// Runtime — Cloud Run target -------------------------------------
	"run.location": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Runtime — GKE target -------------------------------------------
	"gke.cluster": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"gke.internal_ip": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"gke.proxy_url": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Runtime — Anthos -----------------------------------------------
	"anthos_cluster.membership": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Runtime — Multi-target ----------------------------------------
	"multi_target.target_ids": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Runtime — Custom target --------------------------------------
	"custom_target.custom_target_type": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Execution configs — render/deploy/verify worker bindings ---------
	"execution_configs.usages": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"execution_configs.service_account": {
		// A silent rebind here re-purposes the executor identity at the
		// project level — privilege-escalation surface.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"execution_configs.artifact_storage": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"execution_configs.execution_timeout": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"execution_configs.verbose": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"execution_configs.worker_pool": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags / labels / annotations — system-owned -----------------------
	"annotations":           tagPolicy(),
	"effective_annotations": tagPolicy(),
	"labels":                tagPolicy(),
	"effective_labels":      tagPolicy(),
	"terraform_labels":      tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_clouddeploy_target", googleClouddeployTargetPolicy)
}
