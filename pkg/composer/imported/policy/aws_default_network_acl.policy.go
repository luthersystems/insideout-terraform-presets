package policy

// awsDefaultNetworkACLPolicy curates Layer 2 for `aws_default_network_acl`
// (#947).
//
// This is a VPC's default network ACL. Discovery lists it as an
// aws_network_acl, and networkACLPostDiscover
// (cmd/insideout-import/awsdiscover) re-types it to this type, because
// the provider refuses to import a default NACL as aws_network_acl. So
// the curated surface mirrors aws_network_acl.policy.go, with two
// differences that come from how the provider models a default NACL:
//
//   - The ACL is named by the `default_network_acl_id` argument, not
//     by `vpc_id`. AWS creates and deletes the default NACL with its
//     VPC, so the id is pinned: `vpc_id` is computed only.
//   - `subnet_ids` is not managed here. A subnet sits on the default
//     NACL until something associates it with another NACL, so this list
//     moves whenever a custom NACL takes or drops a subnet. It is
//     readable but never edited, and drift on it is not compared.
//
// The `ingress` / `egress` rule sets are the load-bearing security axis,
// compared WholeList, the same as aws_network_acl. (The genconfig fixup
// pins ignore_changes = [egress, ingress] on the first import; see
// docs/reverse-import-contract.md §3.)
var awsDefaultNetworkACLPolicy = Map{
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
	"default_network_acl_id": {
		// The VPC's default NACL id (acl-…). Required by the provider;
		// AWS owns the ACL's lifetime, so it never changes.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent VPC + attached subnets --------------------------
	"vpc_id": {
		// Computed from default_network_acl_id: the VPC that owns it.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet_ids": {
		// The subnets on the default NACL. Changes whenever a custom
		// NACL takes or drops a subnet, so it is not edited here and
		// not compared for drift.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit: EditNever,
	},

	// Tuning — the load-bearing rule sets ------------------------------
	"ingress": {
		// Stateless inbound rule set. Whole-list compare flags any
		// out-of-band rule add / remove / modify as one diff entry.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"egress": {
		// Stateless outbound rule set.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Per-rule fields (per-element semantics for richer diff projection) -
	"ingress.rule_no": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ingress.action": {
		// allow | deny — flipping is the most security-critical drift.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ingress.protocol": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ingress.from_port": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ingress.to_port": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ingress.cidr_block": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ingress.ipv6_cidr_block": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"egress.rule_no": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"egress.action": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"egress.protocol": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"egress.from_port": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"egress.to_port": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"egress.cidr_block": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"egress.ipv6_cidr_block": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_default_network_acl", awsDefaultNetworkACLPolicy)
}
