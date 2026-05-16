package policy

// awsVPCEndpointPolicy curates Layer 2 for `aws_vpc_endpoint`.
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// VPC endpoints provide private connectivity from a VPC to an AWS
// service (Gateway: S3 / DynamoDB; Interface: most others; Resource /
// ServiceNetwork: VPC Lattice). Identity is `id`/`arn`/`service_name`.
// The `vpc_endpoint_type` is fixed at create and gates which knobs
// apply (e.g. `subnet_ids` + `security_group_ids` apply only to Interface
// endpoints, `route_table_ids` apply only to Gateway endpoints).
//
// Drift bundle 3 (#482): scalar attributes use DriftSemanticExact.
// All cross-resource ID-list attachments are order-insensitive sets
// (`subnet_ids`, `security_group_ids`, `route_table_ids`, etc.) — they
// compare WholeList so a missing/extra ID is one diff entry, not N.
// `policy` is a JSON resource-policy document; diffed Exact at this
// layer, canonical-form normalization is the diff projection layer's
// responsibility.
var awsVPCEndpointPolicy = Map{
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
	"prefix_list_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"requester_managed": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"state": {
		// pending | available | rejected | …
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Pinned at create -------------------------------------------------
	"service_name": {
		// e.g. com.amazonaws.us-east-1.s3
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc_endpoint_type": {
		// Gateway | Interface | GatewayLoadBalancer | Resource | ServiceNetwork
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning -----------------------------------------------------------
	"auto_accept": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"private_dns_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ip_address_type": {
		// ipv4 | dualstack | ipv6
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"policy": {
		// Resource policy JSON document.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — attachments (order-insensitive sets) -------------------
	"subnet_ids": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"security_group_ids": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"route_table_ids": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"network_interface_ids": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},
	"cidr_blocks": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// DNS options ------------------------------------------------------
	"dns_options.dns_record_ip_type": {
		// ipv4 | dualstack | ipv6 | service-defined
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"dns_options.private_dns_only_for_inbound_resolver_endpoint": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Subnet configuration (per-subnet IP pins) ----------------------
	"subnet_configuration.subnet_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet_configuration.ipv4": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet_configuration.ipv6": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton -----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_vpc_endpoint", awsVPCEndpointPolicy)
}
