package policy

// awsResourceexplorer2ViewPolicy curates Layer 2 for `aws_resourceexplorer2_view`.
//
// Resource Explorer 2 views filter what an index returns to Search calls
// — they are operationally meaningful (the filter string and the
// included_property list shape every downstream Search query). The
// schema:
//
//   - `arn` / `id` / `name` — identity.
//   - `default_view` — Tuning bool that marks this view as the account
//     default. Changing it is a meaningful operator action
//     (PillarReliability — a downstream query against the default view
//     suddenly returns a different result set).
//   - `filters.filter_string` — Tuning string controlling what the view
//     surfaces. Exact comparison; an unintended drift would silently
//     change downstream Search semantics (PillarSecurity adjacent: a
//     filter that loosens scope leaks resources into a tenant view).
//   - `included_property.name` — Tuning enum; controls which extra
//     fields the view returns. Exact.
//   - `tags` / `tags_all` — uniform tagPolicy() treatment.
//
// All curated leaves are scalar; DriftSemanticExact is the meaningful
// comparison. The two nested-block fields each have a single
// non-computed leaf, so the parent block has no curation entry.
var awsResourceexplorer2ViewPolicy = Map{
	// Identity
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"default_view": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"filters.filter_string": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"included_property.name": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_resourceexplorer2_view", awsResourceexplorer2ViewPolicy)
}
