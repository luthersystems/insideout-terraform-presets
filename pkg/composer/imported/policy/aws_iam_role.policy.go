package policy

// awsIamRolePolicy curates Layer 2 for `aws_iam_role`. Cloud-control-routed
// enrichment was already in place (#482); this adds the typed Layer 1
// struct + curated field map needed to flip the type from Enrichable to
// DriftDetectable in SUPPORTED_RESOURCES.md.
//
// IAM identity rules:
//   - `name` is the primary key. Changing it forces replacement.
//   - `arn`, `id`, `unique_id`, `create_date` are computed identifiers.
//   - `assume_role_policy` is the security-critical trust document; we
//     diff it Exact so out-of-band edits (a stale role trusting a
//     deleted service principal) surface.
//
// Drift bundle (#482): scalar attributes use DriftSemanticExact. The
// `managed_policy_arns` list is order-insensitive in practice but we
// compare WholeList to catch a missing-or-extra attachment as one
// diff entry rather than per-element noise. Tags use the system
// tagPolicy() with DriftSemanticNone — tag drift is filtered upstream.
var awsIamRolePolicy = Map{
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
	"unique_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"create_date": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"path": {
		// IAM path is part of the role's identity — changing it forces
		// recreate. UI-visible for context but not agent-editable.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — trust + cross-resource references ------------------------
	"assume_role_policy": {
		// Trust policy JSON. Security-critical — RequiresApproval lets the
		// interactive agent draft changes but a human confirms the diff.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"permissions_boundary": {
		// Cross-resource pointer to a managed policy ARN.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"managed_policy_arns": {
		// Order-insensitive set of policy ARNs; per-element diff is not
		// meaningfully scoped, so compare the whole list.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tuning ------------------------------------------------------------
	"description": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"max_session_duration": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"force_detach_policies": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Inline policies ---------------------------------------------------
	// IAM provider 5.x deprecates writing inline_policy via aws_iam_role;
	// state can still surface them so curate the leaves for diff coverage.
	"inline_policy.name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"inline_policy.policy": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_iam_role", awsIamRolePolicy)
}
