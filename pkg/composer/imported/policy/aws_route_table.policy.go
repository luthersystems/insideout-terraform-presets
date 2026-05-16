package policy

// awsRouteTablePolicy curates Layer 2 for `aws_route_table`. Cloud-
// control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A route table is the VPC's per-subnet routing config. Identity is
// (id, arn, owner_id). Wiring is `vpc_id`. The `route` block-list is
// the operational drift axis — each entry routes a CIDR to a target
// (igw / nat / tgw / vpce / peering / eni). The `propagating_vgws`
// list flips BGP propagation from attached VGWs.
//
// Drift bundle 6 (#482): scalars use DriftSemanticExact. The `route`
// block-list and `propagating_vgws` use DriftSemanticWholeList. Tags
// use tagPolicy().
var awsRouteTablePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"owner_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — VPC -------------------------------------------------------
	"vpc_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — routes --------------------------------------------------
	"route": {
		// The full set of CIDR -> target hops. Editing reshapes
		// network reachability for every associated subnet.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"propagating_vgws": {
		// VGW-propagated routes (BGP from on-prem / Direct Connect).
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ------------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_route_table", awsRouteTablePolicy)
}
