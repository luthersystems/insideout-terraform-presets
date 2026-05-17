package policy

// awsElasticacheSubnetGroupPolicy curates Layer 2 for
// `aws_elasticache_subnet_group`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// An ElastiCache subnet group is the VPC-wiring sibling to a
// replication group: it pins which subnets the cache cluster's nodes
// can be placed in. Identity is (arn, id, name). `subnet_ids` is the
// load-bearing wiring axis; `vpc_id` is server-derived from the subnets.
//
// Drift bundle 10 (#482): scalars use DriftSemanticExact;
// `subnet_ids` is a list marked DriftSemanticWholeList so add/remove of
// subnets flags as drift. Tags use tagPolicy().
var awsElasticacheSubnetGroupPolicy = Map{
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
		// Subnet-group name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — subnets + derived VPC -----------------------------------
	"subnet_ids": {
		// Subnet pool from which ElastiCache picks node placement.
		// Adding/removing changes the AZ footprint.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_id": {
		// Server-derived from the subnets. Observability only — read-only.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — description --------------------------------------------
	"description": {
		// Free-text description.
		Role: RoleTuning, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_elasticache_subnet_group", awsElasticacheSubnetGroupPolicy)
}
