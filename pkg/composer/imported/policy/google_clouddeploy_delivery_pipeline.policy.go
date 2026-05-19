package policy

// googleClouddeployDeliveryPipelinePolicy curates Layer 2 for
// `google_clouddeploy_delivery_pipeline`.
//
// #623 backfill for the gcp/cloud_deploy preset (#613 / #614). A
// delivery pipeline is the ordered promotion graph a Cloud Deploy
// release walks through (typical: staging → prod). The serial_pipeline
// stage list pins the target chain by name; an out-of-band edit that
// re-orders, adds, or removes a stage rewrites the promotion contract
// and is a high-value drift signal — hence the stages-block curation
// uses `DriftSemanticWholeList` at the list level plus `Exact` on the
// stable per-stage `target_id` so the comparator surfaces both
// re-ordering and per-element mutation.
//
// `suspended` is a security-pillar tuning knob (a paused pipeline is
// indistinguishable at the release-API level from an attacker-blocked
// one); curated Exact so silent flips are visible.
var googleClouddeployDeliveryPipelinePolicy = Map{
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

	// Wiring — project / location are cross-resource anchors -----------
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

	// Tuning -----------------------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"suspended": {
		// A paused pipeline blocks every release promotion. Silent flips
		// look identical to a denial-of-service at the release API; surface
		// as drift.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Stages — the promotion graph -------------------------------------
	// The list is order-sensitive (release walks targets in the authored
	// order). Curating the list itself as WholeList captures stage
	// add/remove/re-order; the per-stage target_id leaf is the stable
	// pointer back into google_clouddeploy_target.name so a name swap
	// also surfaces.
	"serial_pipeline.stages": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"serial_pipeline.stages.target_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"serial_pipeline.stages.profiles": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
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
	Register("google_clouddeploy_delivery_pipeline", googleClouddeployDeliveryPipelinePolicy)
}
