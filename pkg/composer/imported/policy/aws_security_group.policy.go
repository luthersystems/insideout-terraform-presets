package policy

// awsSecurityGroupPolicy curates Layer 2 for `aws_security_group`. Cloud-
// control enrichment already produces the typed Attrs; this map adds the
// curated surface to flip the type to DriftDetectable.
//
// Security group identity is (name, vpc_id) — both pin recreate semantics.
// The `ingress` / `egress` rule lists are the security surface; we
// compare them WholeList so an unexpected open port shows up as a single,
// loud diff entry rather than as a scatter of per-leaf signals.
//
// Drift bundle (#482): scalar attributes use DriftSemanticExact;
// `ingress` / `egress` use DriftSemanticWholeList. Tags stay
// DriftSemanticNone via tagPolicy().
var awsSecurityGroupPolicy = Map{
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
	"owner_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — security group lives in a VPC ---------------------------
	"vpc_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning -----------------------------------------------------------
	"description": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"revoke_rules_on_delete": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Rule lists — the security surface --------------------------------
	// Compared WholeList: ingress/egress rules are the real security
	// state; an unexpected open port is one diff entry, not many.
	"ingress": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"egress": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
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
	Register("aws_security_group", awsSecurityGroupPolicy)
}
