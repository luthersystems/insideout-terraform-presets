package policy

// awsCloudwatchDashboardPolicy curates Layer 2 for
// `aws_cloudwatch_dashboard`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// A CloudWatch dashboard renders an operator-facing collection of
// metric / log / alarm widgets. Identity is (dashboard_arn, id,
// dashboard_name). `dashboard_body` is the load-bearing JSON payload —
// the entire widget set, queries, layout. Out-of-band edits in the AWS
// console show as drift on the body, which is the highest-signal
// regression (lost queries, misaligned axes, deleted widgets).
//
// Drift bundle 10 (#482): scalars use DriftSemanticExact. No tag
// surface — CloudWatch dashboards are not directly tagged.
var awsCloudwatchDashboardPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"dashboard_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"dashboard_name": {
		// Dashboard name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Body — the load-bearing payload ---------------------------------
	"dashboard_body": {
		// JSON describing the widget set. Exact-string drift catches
		// console-edits, lost queries, layout changes. ChatSafe — agents
		// may rearrange widgets without escalating.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cloudwatch_dashboard", awsCloudwatchDashboardPolicy)
}
