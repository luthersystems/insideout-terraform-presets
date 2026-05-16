package policy

// awsBackupPlanPolicy curates Layer 2 for `aws_backup_plan`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Backup plan defines schedule + retention rules for AWS Backup. Each
// rule routes recovery points to a target vault. Identity is (arn, id,
// name). The `version` is computed and increments on each update.
//
// Drift bundle 7 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy(). The nested `rule[]` and `advanced_backup_setting[]`
// blocks are left uncurated — block-level drift is a follow-up, scalar
// drift on identity + version covers the highest-signal cases (someone
// removed/replaced the plan or rotated rules out-of-band).
var awsBackupPlanPolicy = Map{
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
		// Backup plan name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		// Monotonically incrementing — bumps on every rule edit. Drift
		// on this flags out-of-band changes to the plan.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_backup_plan", awsBackupPlanPolicy)
}
