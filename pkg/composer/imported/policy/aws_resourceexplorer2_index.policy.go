package policy

// awsResourceexplorer2IndexPolicy curates Layer 2 for `aws_resourceexplorer2_index`.
//
// Resource Explorer 2 indexes are account-level setup primitives — at most
// one index per region per account. The schema is tiny:
//
//   - `arn` / `id` — identity; the ARN's trailing UUID is unstable across
//     recreate cycles but the resource is keyed by region in TF state.
//   - `type` — Tuning. LOCAL vs AGGREGATOR drives cross-region search
//     semantics; flipping it is an in-place update but materially changes
//     the data the index returns to Search calls (PillarPerformance).
//   - `tags` / `tags_all` — uniform tagPolicy() treatment.
//   - `timeouts` — Terraform internal apply-time bookkeeping; not part
//     of the operational surface, no curation.
//
// All curated leaves are scalar; DriftSemanticExact is the meaningful
// comparison.
var awsResourceexplorer2IndexPolicy = Map{
	// Identity
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_resourceexplorer2_index", awsResourceexplorer2IndexPolicy)
}
