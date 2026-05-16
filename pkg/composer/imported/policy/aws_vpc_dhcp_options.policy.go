package policy

// awsVPCDhcpOptionsPolicy curates Layer 2 for `aws_vpc_dhcp_options`.
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A DHCP option set defines the DNS / NTP / NetBIOS / domain-name
// metadata VPC instances receive at lease time. Identity is (id, arn,
// owner_id). The DNS-server list is the highest-signal axis — swapping
// out resolvers silently redirects intra-VPC name resolution.
//
// Drift bundle 9 (#482): scalars use DriftSemanticExact; list-shaped
// resolver attributes compare WholeList.
var awsVPCDhcpOptionsPolicy = Map{
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

	// Tuning — DNS / NTP / NetBIOS resolver pointers -------------------
	"domain_name": {
		// The DNS suffix instances append for unqualified lookups.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"domain_name_servers": {
		// DNS resolvers handed out via DHCP. Whole-list compare so
		// an added/removed resolver is one diff entry.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"ntp_servers": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"netbios_name_servers": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"netbios_node_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"ipv6_address_preferred_lease_time": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_vpc_dhcp_options", awsVPCDhcpOptionsPolicy)
}
