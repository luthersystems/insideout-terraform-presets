package policy

// awsNatGatewayPolicy curates Layer 2 for `aws_nat_gateway`. Cloud-
// control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A NAT gateway is the VPC's outbound NAT for private subnets. Identity
// is (id, association_id, network_interface_id). Wiring axes are
// `subnet_id` (where the NAT lives) and `allocation_id` (the EIP it
// fronts). `connectivity_type` (public/private) is pinned at create.
//
// Drift bundle 6 (#482): scalars use DriftSemanticExact; the secondary
// allocation/IP lists use DriftSemanticWholeList. Tags use tagPolicy().
var awsNatGatewayPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"association_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"network_interface_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"private_ip": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"public_ip": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — the EIP and subnet --------------------------------------
	"allocation_id": {
		// The EIP allocation this NAT fronts. Swapping rotates the
		// NAT's egress public IP — operator-only.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet_id": {
		// The (public) subnet the NAT gateway lives in. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — connectivity, secondary IPs -----------------------------
	"connectivity_type": {
		// "public" (default) vs "private". Pinned at create.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"secondary_allocation_ids": {
		// Additional EIPs attached to the NAT for higher port allocation.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"secondary_private_ip_address_count": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"secondary_private_ip_addresses": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
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
	Register("aws_nat_gateway", awsNatGatewayPolicy)
}
