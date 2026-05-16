package policy

// awsIAMRolePolicyPolicy curates Layer 2 for `aws_iam_role_policy`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// `aws_iam_role_policy` is an inline policy attached directly to an IAM
// role (the standalone alternative to a managed policy + attachment).
// Identity is (role × name); the `policy` document is the security-
// critical blob carrying the actual statements. Drift on the document
// flags an out-of-band policy edit — the highest-signal IAM regression.
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact. The `policy`
// field is treated as an opaque text blob (JSON) — exact-string drift
// catches statement additions, action expansions, and resource scope
// widening. No tag surface — IAM inline policies don't carry tags.
var awsIAMRolePolicyPolicy = Map{
	// Identity ----------------------------------------------------------
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

	// Wiring — parent role --------------------------------------------
	"role": {
		// Name (not ARN) of the IAM role this inline policy is attached
		// to. Pinned at create.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Policy document — the security-critical blob -------------------
	"policy": {
		// IAM policy JSON. Exact-string drift catches out-of-band edits
		// (statement additions, action expansions, resource widening).
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_iam_role_policy", awsIAMRolePolicyPolicy)
}
