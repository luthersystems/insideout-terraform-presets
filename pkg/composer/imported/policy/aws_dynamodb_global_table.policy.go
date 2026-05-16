package policy

// awsDynamodbGlobalTablePolicy curates Layer 2 for `aws_dynamodb_global_table`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A DynamoDB Global Table (V1) is the multi-region replication group
// over the same table name in N regions. Identity is (name, arn).
// `replica.region_name` is the set of regions hosting a replica — the
// load-bearing attribute on this resource type.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact; the
// `replica` block is a list-shaped attribute compared WholeList — an
// unexpected add/remove of a region is one diff entry.
var awsDynamodbGlobalTablePolicy = Map{
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

	// Wiring — replica set (the load-bearing config) -----------------
	"replica": {
		// List of {region_name} entries — the set of regions that hold a
		// replica of the table. Whole-list compare so adding/removing a
		// region surfaces as one diff entry.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"replica.region_name": {
		// Per-replica region pointer. Identity within the replica set.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_dynamodb_global_table", awsDynamodbGlobalTablePolicy)
}
