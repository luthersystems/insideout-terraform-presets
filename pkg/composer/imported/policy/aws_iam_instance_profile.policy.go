package policy

// awsIamInstanceProfilePolicy curates Layer 2 for
// `aws_iam_instance_profile`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// An instance profile is a container that lets EC2 instances assume an
// IAM role. Identity is (name, arn, unique_id). The wiring axis is the
// `role` reference — flipping it changes the effective permissions of
// every EC2 instance carrying this profile.
//
// Drift bundle 6 (#482): scalars use DriftSemanticExact. The bound role
// is RoleWiring + RequiresApproval. Tags use tagPolicy().
var awsIamInstanceProfilePolicy = Map{
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
		// IAM path is part of the instance profile's identity.
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
	},

	// Wiring — bound IAM role ------------------------------------------
	"role": {
		// The IAM role this instance profile lets EC2 assume. Security-
		// critical: swapping the role rotates every EC2 instance's
		// effective permissions.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_iam_instance_profile", awsIamInstanceProfilePolicy)
}
