package policy

// googleCloudfunctions2FunctionIAMMemberPolicy curates the Layer 2
// axes for the google_cloudfunctions2_function_iam_member resource.
// Identical shape to google_cloud_run_v2_service_iam_member — Cloud
// Functions Gen-2 and Cloud Run v2 share the IAM-member resource
// schema modulo the `cloud_function` name field.
var googleCloudfunctions2FunctionIAMMemberPolicy = Map{
	"id":             {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"etag":           {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"cloud_function": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"location":       {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"project":        {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"role":           {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"member":         {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
}

func init() {
	Register("google_cloudfunctions2_function_iam_member", googleCloudfunctions2FunctionIAMMemberPolicy)
}
