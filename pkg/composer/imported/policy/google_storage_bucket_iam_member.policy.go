package policy

// googleStorageBucketIAMMemberPolicy curates the Layer 2 axes for the
// google_storage_bucket_iam_member resource. Mirrors the
// google_project_iam_member policy shape — IAM-binding resources have
// a small, fully-identity surface.
//
// Drift bundle (#491): role + member are Exact — an out-of-band IAM
// edit that flips either is a real security event (especially for a
// publicly-readable GCS bucket). bucket is Exact for completeness;
// id / etag stay DriftSemantic=None.
var googleStorageBucketIAMMemberPolicy = Map{
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"etag": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"bucket": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"role": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"member": {
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("google_storage_bucket_iam_member", googleStorageBucketIAMMemberPolicy)
}
