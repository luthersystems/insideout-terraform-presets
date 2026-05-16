package policy

// awsInternetGatewayPolicy curates Layer 2 for `aws_internet_gateway`.
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An IGW is the VPC's public-internet egress/ingress edge. Identity is
// (id, arn, owner_id). The wiring axis is `vpc_id` — attach/detach
// flips internet reachability of every public subnet in the VPC.
//
// Drift bundle 6 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy().
var awsInternetGatewayPolicy = Map{
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

	// Wiring — attached VPC --------------------------------------------
	"vpc_id": {
		// The VPC this IGW is attached to. Detaching strands every
		// public subnet's outbound internet path. Security-critical.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ------------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_internet_gateway", awsInternetGatewayPolicy)
}
