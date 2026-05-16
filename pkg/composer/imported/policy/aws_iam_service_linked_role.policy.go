package policy

// awsIAMServiceLinkedRolePolicy curates Layer 2 for
// `aws_iam_service_linked_role`. Cloud-control-routed enrichment
// already produces typed Attrs; this map adds the curated surface to
// flip the type from Enrichable to DriftDetectable.
//
// A service-linked role is an AWS-managed IAM role auto-created for an
// AWS service principal (e.g. autoscaling.amazonaws.com,
// elasticloadbalancing.amazonaws.com). Identity is (arn, id, name,
// unique_id). `aws_service_name` pins which AWS service the role
// authorizes — drift on it effectively means a different role.
// `custom_suffix` is the optional name disambiguator (some service
// principals allow multiple SLRs per account, distinguished by suffix).
//
// Drift bundle 10 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy(). The trust policy is server-managed and not exposed via
// a discrete attribute (AWS owns it).
var awsIAMServiceLinkedRolePolicy = Map{
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
		// AWS-assigned role name (AWSServiceRoleFor<Service>[_<suffix>]).
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"unique_id": {
		// IAM-internal stable ID. Server-assigned.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — service principal + suffix -----------------------------
	"aws_service_name": {
		// The AWS service principal (e.g. autoscaling.amazonaws.com)
		// this role authorizes. Drift means a different role.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"custom_suffix": {
		// Optional name disambiguator. Pinned at create.
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"path": {
		// IAM path (`/aws-service-role/...` for SLRs). Server-assigned.
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — description --------------------------------------------
	"description": {
		// Free-text description visible in the IAM console.
		Role: RoleTuning, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_iam_service_linked_role", awsIAMServiceLinkedRolePolicy)
}
