package policy

// awsApprunnerVPCConnectorPolicy curates Layer 2 for
// `aws_apprunner_vpc_connector`.
//
// #623 backfill for the aws/apprunner preset (#598 / #620). A VPC
// connector is the egress anchor for an App Runner service that needs
// to reach private resources (RDS, ElastiCache, internal ALB). It pins
// subnets + security_groups; the connector itself is immutable, and
// any subnet / SG change spawns a new revision. Out-of-band edits to
// the subnet / SG sets — or to the underlying SG's rules elsewhere —
// re-shape the egress topology, so both lists are curated with
// `DriftSemanticWholeList`.
var awsApprunnerVPCConnectorPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc_connector_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc_connector_revision": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"status": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — subnets + SGs ------------------------------------------
	"subnets": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"security_groups": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_apprunner_vpc_connector", awsApprunnerVPCConnectorPolicy)
}
