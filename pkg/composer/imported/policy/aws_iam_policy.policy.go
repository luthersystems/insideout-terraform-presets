package policy

// awsIamPolicyPolicy curates Layer 2 for `aws_iam_policy` (a managed IAM
// policy document). Cloud-control-routed enrichment already produced the
// typed Attrs; this map gives the drift comparator a curated surface so
// the type flips from Enrichable to DriftDetectable.
//
// Security pillar throughout — every leaf influences who-can-do-what.
// The policy JSON itself is RequiresApproval: the interactive agent can
// draft text but a human confirms the diff before it applies.
//
// Drift bundle (#482): every curated leaf is scalar, so DriftSemanticExact
// is the meaningful comparison. Tags stay DriftSemanticNone via
// tagPolicy().
var awsIamPolicyPolicy = Map{
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
	"path": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"policy_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — the policy document is the security surface --------------
	"policy": {
		// JSON policy document. Out-of-band edits = real security drift.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning ------------------------------------------------------------
	"description": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"attachment_count": {
		// Computed counter — informational only, but useful drift signal
		// (if it changes, attachments drifted).
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_iam_policy", awsIamPolicyPolicy)
}
