package policy

// awsBackupSelectionPolicy curates Layer 2 for `aws_backup_selection`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Backup selection binds an AWS Backup plan to the set of resources it
// should back up. Identity is (plan_id, id, name). Resource scoping is
// driven by three orthogonal axes: `resources` (explicit ARN list),
// `not_resources` (exclusion ARNs), and the `selection_tag[]` /
// `condition[]` blocks (tag-based dynamic scoping). `iam_role_arn` is
// the Backup service-execution role used to read resource state.
//
// Drift bundle 10 (#482): scalar attributes use DriftSemanticExact;
// resources / not_resources are tracked as DriftSemanticWholeList so
// any addition/removal of in-scope ARNs flags as drift. The nested
// `selection_tag[]` and `condition[]` blocks are left uncurated —
// block-level drift is a follow-up. Selections do not carry their own
// tags.
var awsBackupSelectionPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Selection name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent plan + service role -----------------------------
	"plan_id": {
		// Pointer to the parent aws_backup_plan. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"iam_role_arn": {
		// Backup service-execution role. Retargeting can silently change
		// which resources Backup is allowed to read — RequiresApproval.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Scope — explicit ARN sets ---------------------------------------
	"resources": {
		// Explicit ARN inclusion list. Adding/removing here changes the
		// set of resources Backup snapshots.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"not_resources": {
		// Explicit ARN exclusion list. Subtractive override of `resources`
		// / tag-based selection.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
}

func init() {
	Register("aws_backup_selection", awsBackupSelectionPolicy)
}
