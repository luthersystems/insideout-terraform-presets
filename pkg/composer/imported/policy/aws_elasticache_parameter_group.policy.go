package policy

// awsElasticacheParameterGroupPolicy curates Layer 2 for
// `aws_elasticache_parameter_group`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// An ElastiCache parameter group is an engine-family parameter set that
// ElastiCache replication groups / clusters reference. Identity is
// (name, family, arn). The `parameter` block (nested, list of
// {name,value}) is the load-bearing tuning surface — drift on it changes
// the engine's runtime configuration silently.
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy(). The nested `parameter` block is left uncurated at the
// scalar policy layer — block-level drift is covered by the composed
// Attrs diff at a higher layer.
var awsElasticacheParameterGroupPolicy = Map{
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
		// Parameter-group name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — engine family ------------------------------------------
	"family": {
		// Engine family (e.g. redis7, memcached1.6); pinned at create —
		// changing family is a replace.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — description --------------------------------------------
	"description": {
		// Human-readable description; safe to update.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_elasticache_parameter_group", awsElasticacheParameterGroupPolicy)
}
