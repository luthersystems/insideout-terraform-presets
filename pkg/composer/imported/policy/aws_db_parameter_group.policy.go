package policy

// awsDbParameterGroupPolicy curates Layer 2 for `aws_db_parameter_group`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A DB parameter group is an engine-family parameter set RDS DB
// instances reference via parameter_group_name. Identity is (arn, id,
// name); `family` is the engine version (e.g. mysql8.0) and is pinned at
// create. The nested `parameter[]` block (name/value/apply_method
// tuples) is left uncurated — block-level drift is a follow-up. Scalar
// drift on (arn, id, name, family, description, skip_destroy) covers
// the highest-signal axes.
//
// Drift bundle 7 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy().
var awsDbParameterGroupPolicy = Map{
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
	"name_prefix": {
		// Optional name-prefix companion to name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Engine family + behavior -----------------------------------------
	"family": {
		// RDS engine family (e.g. mysql8.0, postgres16). Pinned at create.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"skip_destroy": {
		// Provider-side flag — if set, the resource is not deleted on
		// terraform destroy.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_db_parameter_group", awsDbParameterGroupPolicy)
}
