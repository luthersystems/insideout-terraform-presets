package policy

// googleCloudRunV2ServiceIAMMemberPolicy curates the Layer 2 axes for
// the google_cloud_run_v2_service_iam_member resource. The (name ×
// location × role × member) tuple is identity; all fields are
// non-editable since the row is replaced rather than mutated.
var googleCloudRunV2ServiceIAMMemberPolicy = Map{
	"id":       {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"etag":     {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"name":     {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"location": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"project":  {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"role":     {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"member":   {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
}

func init() {
	Register("google_cloud_run_v2_service_iam_member", googleCloudRunV2ServiceIAMMemberPolicy)
}
