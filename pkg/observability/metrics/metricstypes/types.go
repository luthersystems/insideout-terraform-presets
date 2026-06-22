// Package metricstypes holds the SDK-free result/data types for the
// get-metrics path. It is a leaf package with ZERO cloud-SDK imports
// (no aws-sdk-go-v2/*, no cloud.google.com/go/*) so a consumer that
// only needs to *deserialize* a MetricsResult — e.g. luthersystems/
// reliable's thin proxy after the observability reads moved into
// ui-core (reliable#2153) — can do so without dragging the CloudWatch
// / Cloud Monitoring clients into its binary.
//
// The parent pkg/observability/metrics package re-exports every type
// here as a Go type alias, so existing callers of metrics.MetricsResult
// etc. are unaffected and the wire shape is byte-for-byte identical.
//
// JSON tags below are bit-for-bit identical to the InsideOut backend's
// original definitions so the wire shape returned to InsideOut / Oracle
// frontends is preserved across the migration window. Do NOT change a
// field name or json tag without a coordinated wire-breaking rollout
// across reliable + ui-core.
package metricstypes

// MetricsFilter controls the time range and granularity of metric
// queries. Defaults (Hours=6, Period=300) are applied by
// metrics.ParseMetricsFilter; the public Fetch entry point assumes the
// caller has already set them.
type MetricsFilter struct {
	Hours  int `json:"hours"`  // lookback window (default: 6)
	Period int `json:"period"` // aggregation period in seconds (default: 300)

	// AccountID is the caller's AWS account ID. It's the dimension VALUE
	// for groups whose AWSObs.DimensionValueAccountID is set — today only
	// the AOSS OCU group (AWS/AOSS publishes account-level OCU under a
	// ClientId=<account-id> dimension, #778). The inspector that resolves
	// the credential already knows the account (sts.GetCallerIdentity at
	// dispatch); it rides here so Fetch need not make its own STS call.
	// Empty for callers fetching only account-keyed-free services; the
	// query builder then skips any account-keyed group rather than
	// emitting an empty-value query.
	AccountID string `json:"account_id,omitempty"`
}

// MetricsResult is the top-level response for a get-metrics action.
type MetricsResult struct {
	Service   string            `json:"service"`
	TimeRange string            `json:"time_range"`
	Period    int               `json:"period_seconds"`
	Resources []ResourceMetrics `json:"resources"`
}

// ResourceMetrics holds metrics for a single resource (e.g. one EC2
// instance).
type ResourceMetrics struct {
	ResourceID string         `json:"resource_id"`
	Metrics    []MetricSeries `json:"metrics"`
}

// MetricSeries holds the data for a single metric.
//
// The JSON shape ("name", "unit", "datapoints") is preserved unchanged
// for AWS; "labels" is the GCP-side breakdown surface. AWS callers
// leave Labels nil and Unit string; GCP callers leave Unit empty and
// populate Labels for breakdown metrics (e.g. cloudfunctions
// execution_count by status=ok/error). Both shapes co-exist on the
// wire with omitempty.
type MetricSeries struct {
	Name       string            `json:"name"`
	Unit       string            `json:"unit,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Datapoints []Datapoint       `json:"datapoints"`
}

// Datapoint is a single metric value at a point in time. The field name
// "Average" is a historical accident — the CloudWatch Stat (Average /
// Sum / Maximum) is chosen per-metric in the spec table; this field
// carries whichever statistic was requested. Renaming is a
// downstream-API breaking change deferred indefinitely.
type Datapoint struct {
	Timestamp string  `json:"timestamp"`
	Average   float64 `json:"average"`
}

// ResourceID is a resource identifier for the metric-fetch path. The
// CloudWatch dimension name (e.g. "InstanceId", "DBInstanceIdentifier")
// is carried alongside the value so a single Fetch call can serve a
// service whose dimension name is service-fixed but whose dimension
// values are caller-supplied.
//
// In practice every resource passed to a single Fetch invocation
// shares the same DimensionName (the one declared on the AWSObs spec).
// The struct keeps the pair colocated so callers don't have to thread
// a parallel string through.
type ResourceID struct {
	ID            string
	DimensionName string
}
