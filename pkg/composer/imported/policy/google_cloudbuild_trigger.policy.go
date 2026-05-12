package policy

var googleCloudbuildTriggerPolicy = Map{
	// Identity
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"trigger_id": {
		Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},

	// Wiring — service account for build execution.
	"service_account": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly,
	},

	// Tuning — top-level.
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"disabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
	},
	"filename": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval,
	},
	"filter": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"include_build_logs": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"ignored_files": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"included_files": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	// NB: `tags` on cloudbuild_trigger is NOT labels — it's a free-text
	// set of operator annotations (per provider docs). Same lint
	// trap as google_compute_instance.tags: lint.go's tagAttrSuffixes
	// hardcodes "tags" as label-shaped, so any non-SystemOnly curation
	// trips CodeTagFieldNotSystemOnly. Intentionally uncurated until
	// the lint exemption lands (paired with the compute_instance
	// follow-up).
	"substitutions": tagPolicy(),

	// GitHub trigger source.
	"github.owner": {
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
	},
	"github.name": {
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
	},
	"github.push.branch": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"github.push.tag": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"github.pull_request.branch": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
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
