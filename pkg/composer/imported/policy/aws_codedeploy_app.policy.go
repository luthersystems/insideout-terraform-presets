package policy

// awsCodedeployAppPolicy curates Layer 2 for `aws_codedeploy_app`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A CodeDeploy Application groups deployment groups for a single
// compute_platform (Server / Lambda / ECS). The compute_platform is
// fixed at create time; name uniquely identifies the application within
// the account.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact. The
// type has no list-shaped fields. Tags use tagPolicy().
var awsCodedeployAppPolicy = Map{
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
	"application_id": {
		// Computed CodeDeploy-side identifier.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — compute_platform (pinned at create) --------------------
	"compute_platform": {
		// "Server" | "Lambda" | "ECS". Forces replacement.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// GitHub integration (legacy Server platform) ---------------------
	"github_account_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"linked_to_github": {
		// Computed — informational flag.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_codedeploy_app", awsCodedeployAppPolicy)
}
