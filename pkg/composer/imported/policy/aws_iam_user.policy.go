package policy

// awsIamUserPolicy curates Layer 2 for `aws_iam_user`. Cloud-control-routed
// enrichment already produces typed Attrs; this map adds the curated
// surface to flip the type from Enrichable to DriftDetectable.
//
// Standalone IAM users are used for machine identities and cross-account
// human access not modeled through roles. Identity is (name, arn,
// unique_id). The security-critical knob is `permissions_boundary` (the
// policy ARN capping the user's effective permissions) — flipping or
// dropping it is the canonical compliance regression.
//
// Drift bundle 5 (#482): scalar attributes use DriftSemanticExact. The
// resource itself has no list-shaped attrs (group memberships and
// attached managed policies are separate aws_iam_user_group_membership
// / aws_iam_user_policy_attachment resources). Tags use tagPolicy().
var awsIamUserPolicy = Map{
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
	"unique_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"path": {
		// IAM path is part of the user's identity — changing forces recreate.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — permissions boundary -----------------------------------
	"permissions_boundary": {
		// Pointer to a managed policy ARN capping the user's effective
		// permissions. Security-critical.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning ------------------------------------------------------------
	"force_destroy": {
		// Whether deletion is allowed even when the user has attached
		// access keys / login profiles. Operator-only.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_iam_user", awsIamUserPolicy)
}
