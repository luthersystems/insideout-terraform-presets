package policy

// googleCloudbuildTriggerPolicy curates Layer 2 for `google_cloudbuild_trigger`.
//
// Bundle D3 (#482): DriftSemantic axis added — scalar identity (name,
// project, location, trigger_id) + scalar config (filename, disabled,
// service_account, github wiring) use DriftSemanticExact. List-valued
// path filters (ignored_files, included_files) use DriftSemanticWholeList
// — order is irrelevant on the provider side but per-element diffs are
// not independently actionable (a missing glob is a missing filter rule).
var googleCloudbuildTriggerPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
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
	"trigger_id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — service account for build execution.
	"service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — top-level.
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"disabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"filename": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"filter": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"include_build_logs": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ignored_files": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"included_files": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	// NB: `tags` on cloudbuild_trigger is NOT labels — it's a free-text
	// set of operator annotations (per provider docs). Same lint
	// trap as google_compute_instance.tags: lint.go's tagAttrSuffixes
	// hardcodes "tags" as label-shaped, so any non-SystemOnly curation
	// trips CodeTagFieldNotSystemOnly. Intentionally uncurated until
	// the lint exemption lands (paired with the compute_instance
	// follow-up).
	// Substitutions are user-supplied build variables — operator-
	// controlled config, not labels. Values may carry secrets, so
	// SensitivityRedacted keeps them out of raw diffs while leaving
	// the keys visible. EditRequiresApproval because changes affect
	// downstream build behavior.
	"substitutions": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, Sensitivity: SensitivityRedacted,
	},

	// GitHub trigger source.
	"github.owner": {
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"github.name": {
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"github.push.branch": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"github.push.tag": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"github.pull_request.branch": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Webhook trigger source.
	"webhook_config.secret": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, Sensitivity: SensitivityRedacted,
	},

	// Pub/Sub trigger source.
	"pubsub_config.topic": {
		Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
	},
	"pubsub_config.service_account_email": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Approval gate.
	"approval_config.approval_required": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_cloudbuild_trigger", googleCloudbuildTriggerPolicy)
}
