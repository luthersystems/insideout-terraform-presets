package policy

// googleCloudRunV2ServiceIAMMemberPolicy curates the Layer 2 axes for
// the google_cloud_run_v2_service_iam_member resource. The (name ×
// location × role × member) tuple is identity; all fields are
// non-editable since the row is replaced rather than mutated.
//
// Drift bundle (#491): role + member are Exact — an out-of-band IAM
// edit that flips either is a real security event. project / location
// / name are also Exact for completeness; identical identity on both
// sides simply does not surface a mismatch. id and etag are TF /
// provider scaffolding and stay DriftSemantic=None to avoid noise on
// every refresh.
var googleCloudRunV2ServiceIAMMemberPolicy = Map{
	"id":   {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"etag": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"name": {
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
	Register("google_cloud_run_v2_service_iam_member", googleCloudRunV2ServiceIAMMemberPolicy)
}
