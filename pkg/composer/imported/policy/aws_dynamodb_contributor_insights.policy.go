package policy

// awsDynamodbContributorInsightsPolicy curates Layer 2 for
// `aws_dynamodb_contributor_insights`.
//
// The TF resource is a meta-binding on a DDB table: enabling it
// activates the per-key consumption metrics published to CloudWatch.
// Schema is tiny — id, table_name (required), index_name (optional
// for per-index insights).
//
// Operationally: enabling contributor insights costs $$$ on busy
// tables (CloudWatch metric publishing). Drift here usually signals
// a forgotten cost optimization or, conversely, a disabled feature
// that ought to be on for hot-key debugging.
var awsDynamodbContributorInsightsPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"table_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"index_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_dynamodb_contributor_insights", awsDynamodbContributorInsightsPolicy)
}
