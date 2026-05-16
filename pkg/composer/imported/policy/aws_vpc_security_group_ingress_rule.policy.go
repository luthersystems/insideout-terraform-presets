package policy

// awsVPCSecurityGroupIngressRulePolicy curates Layer 2 for
// `aws_vpc_security_group_ingress_rule`. Cloud-control-routed
// enrichment already produces typed Attrs; this map adds the curated
// surface to flip the type from Enrichable to DriftDetectable.
//
// This is the modern, one-rule-per-resource form for SG ingress (vs.
// the legacy `aws_security_group` embedded `ingress {}` blocks).
// Identity is (security_group_rule_id); the (ip_protocol, from_port,
// to_port) tuple + the source pointer (cidr_ipv4 / cidr_ipv6 /
// prefix_list_id / referenced_security_group_id) is the security-
// critical surface. Silent CIDR widening / protocol expansion is the
// regression class this rule shape catches.
//
// Drift bundle 9 (#482): scalars use DriftSemanticExact.
var awsVPCSecurityGroupIngressRulePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"security_group_rule_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent SG ----------------------------------------------
	"security_group_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — protocol / port range ---------------------------------
	"ip_protocol": {
		// tcp | udp | icmp | -1 | protocol number. Required.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"from_port": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"to_port": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — source pointers (exactly one populated) --------------
	"cidr_ipv4": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"cidr_ipv6": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"prefix_list_id": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"referenced_security_group_id": {
		// Source is a peer SG (intra-VPC mesh).
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Description — operator annotation -------------------------------
	"description": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_vpc_security_group_ingress_rule", awsVPCSecurityGroupIngressRulePolicy)
}
