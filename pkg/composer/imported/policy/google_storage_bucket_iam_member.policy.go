package policy

// googleStorageBucketIAMMemberPolicy curates the Layer 2 axes for the
// google_storage_bucket_iam_member resource. Mirrors the
// google_project_iam_member policy shape — IAM-binding resources have
// a small, fully-identity surface.
var googleStorageBucketIAMMemberPolicy = Map{
	"id":     {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever, DriftSemantic: DriftSemanticExact},
	"etag":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"bucket": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"role":   {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
	"member": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace, DriftSemantic: DriftSemanticExact},
}

func init() {
	Register("google_storage_bucket_iam_member", googleStorageBucketIAMMemberPolicy)
}
