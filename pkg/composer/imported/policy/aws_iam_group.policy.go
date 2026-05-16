package policy

// awsIamGroupPolicy curates Layer 2 for `aws_iam_group`. Cloud-control-
// routed enrichment already produces typed Attrs; this map adds the
// curated surface to flip the type from Enrichable to DriftDetectable.
//
// An IAM group is a named collection of users for shared policy
// attachment. Identity is (name, arn, unique_id). The `path` is part of
// the identity (changing forces recreate). Group membership and policy
// attachments are modeled as separate aws_iam_group_membership /
// aws_iam_group_policy_attachment resources — not visible here.
//
// Drift bundle 6 (#482): scalar identity attributes use
// DriftSemanticExact. No tags surface (IAM groups are untaggable).
var awsIamGroupPolicy = Map{
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
		// IAM path is part of the group's identity — changing forces recreate.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_iam_group", awsIamGroupPolicy)
}
