package policy

// awsCloudfrontMonitoringSubscriptionPolicy curates Layer 2 for
// `aws_cloudfront_monitoring_subscription`. Cloud-control-routed
// enrichment already produces typed Attrs; this map adds the curated
// surface to flip the type from Enrichable to DriftDetectable.
//
// A CloudFront monitoring subscription is a per-distribution singleton
// that toggles additional CloudWatch metrics (realtime origin requests,
// cache-hit/miss, etc.). Identity is the distribution_id. Drift on the
// nested `realtime_metrics_subscription_status` (Enabled / Disabled)
// silently turns on per-distribution metric billing or hides the
// realtime metrics from dashboards.
//
// Drift bundle 12 (#482): scalars use DriftSemanticExact. No tag
// surface — the subscription itself is not tagged (the parent
// distribution carries tags).
var awsCloudfrontMonitoringSubscriptionPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent distribution ------------------------------------
	"distribution_id": {
		// Pointer to the parent aws_cloudfront_distribution. Pinned at
		// create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — realtime metrics toggle --------------------------------
	"monitoring_subscription.realtime_metrics_subscription_config.realtime_metrics_subscription_status": {
		// Enabled / Disabled. Flips per-distribution realtime metric
		// emission and the associated billing line.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cloudfront_monitoring_subscription", awsCloudfrontMonitoringSubscriptionPolicy)
}
