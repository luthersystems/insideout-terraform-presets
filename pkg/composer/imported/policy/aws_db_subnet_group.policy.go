package policy

// awsDbSubnetGroupPolicy curates Layer 2 for `aws_db_subnet_group`.
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An RDS DB subnet group selects which subnets the RDS service can
// place ENIs in (i.e. which AZs RDS instances can land). Identity is
// (name, arn). Wiring axis is `subnet_ids` (the cross-AZ set) and the
// computed `vpc_id`.
//
// Drift bundle 6 (#482): scalars use DriftSemanticExact; `subnet_ids`
// and `supported_network_types` use DriftSemanticWholeList. Tags use
// tagPolicy().
var awsDbSubnetGroupPolicy = Map{
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
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — subnets and VPC ----------------------------------------
	"subnet_ids": {
		// The cross-AZ subnet set RDS can place instance ENIs in.
		// Shrinking removes AZ candidates and can break multi-AZ DBs.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"vpc_id": {
		// Derived from subnet_ids; pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"supported_network_types": {
		// Computed: which RDS network types (IPv4, dual-stack) this
		// subnet group supports.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_db_subnet_group", awsDbSubnetGroupPolicy)
}
