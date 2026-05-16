package policy

// googleProjectIAMMemberPolicy curates the Layer 2 axes for the
// google_project_iam_member resource. IAM-binding resources are
// minimally curated by design — every field is either an immutable
// identity component (project, role, member) or a TF-internal
// computed scalar (id, etag). The conditional binding sub-block is
// deliberately omitted: the discoverer flattens conditional bindings
// into separate rows, so a condition block surfaces as additional
// rows rather than as an editable nested struct here.
var googleProjectIAMMemberPolicy = Map{
	// Identity.
	"id":      {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"etag":    {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"role":    {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
	"member":  {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever, ChangeRisk: ChangeAlwaysReplace},
}

func init() {
	Register("google_project_iam_member", googleProjectIAMMemberPolicy)
}
