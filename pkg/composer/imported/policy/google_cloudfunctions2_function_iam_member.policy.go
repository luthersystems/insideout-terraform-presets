package policy

// googleCloudfunctions2FunctionIAMMemberPolicy curates the Layer 2
// axes for the google_cloudfunctions2_function_iam_member resource.
// Identical shape to google_cloud_run_v2_service_iam_member — Cloud
// Functions Gen-2 and Cloud Run v2 share the IAM-member resource
// schema modulo the `cloud_function` name field.
//
// Drift bundle (#491): role + member are Exact — an out-of-band IAM
// edit that flips either is a real security event. cloud_function /
// project / location are also Exact for completeness. id / etag stay
// DriftSemantic=None.
var googleCloudfunctions2FunctionIAMMemberPolicy = Map{
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"etag": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"cloud_function": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
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
	Register("google_cloudfunctions2_function_iam_member", googleCloudfunctions2FunctionIAMMemberPolicy)
}
