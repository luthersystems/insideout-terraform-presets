// Package metrics is the CloudWatch metric-fetch wrapper ported from
// The InsideOut backend's internal/agentapi/aws_metrics.go. Per-service resource
// discovery (DescribeInstances, ListFunctions, etc.) is intentionally
// NOT included here — that surface lands in C14 alongside the AWS
// inspector port. This package handles the slice of the pipeline that
// runs after callers have already produced a list of resource IDs:
// build CloudWatch GetMetricDataQuery slices, hand them to the
// CloudWatch API, normalize the response into MetricsResult.
//
// JSON tags on every type below are bit-for-bit identical to the InsideOut backend's
// so the wire shape returned to InsideOut/Oracle frontends is preserved
// across the migration window.
package metrics

// MetricsFilter controls the time range and granularity of metric
// queries. Mirrors the InsideOut backend's MetricsFilter (aws_metrics.go:67).
// Defaults (Hours=6, Period=300) are applied by ParseMetricsFilter; the
// public Fetch entry point assumes the caller has already set them.
type MetricsFilter struct {
	Hours  int `json:"hours"`  // lookback window (default: 6)
	Period int `json:"period"` // aggregation period in seconds (default: 300)
}

// MetricsResult is the top-level response for a get-metrics action.
// Mirrors the InsideOut backend's MetricsResult (aws_metrics.go:74).
type MetricsResult struct {
	Service   string            `json:"service"`
	TimeRange string            `json:"time_range"`
	Period    int               `json:"period_seconds"`
	Resources []ResourceMetrics `json:"resources"`
}

// ResourceMetrics holds metrics for a single resource (e.g. one EC2
// instance). Mirrors the InsideOut backend's ResourceMetrics (aws_metrics.go:81).
type ResourceMetrics struct {
	ResourceID string         `json:"resource_id"`
	Metrics    []MetricSeries `json:"metrics"`
}

// MetricSeries holds the data for a single metric.
//
// Renamed from the InsideOut backend's MetricResult (aws_metrics.go:88) to disambiguate
// from MetricsResult and from the Go convention that *Result implies a
// type returned at the top of an API call. The JSON shape ("name",
// "unit", "datapoints") is preserved unchanged for AWS; "labels" is
// the GCP-side breakdown surface ported from the InsideOut backend's GCPMetricResult
// (gcp_metrics.go:98). AWS callers leave Labels nil and Unit string;
// GCP callers leave Unit empty and populate Labels for breakdown
// metrics (e.g. cloudfunctions execution_count by status=ok/error).
// Both shapes co-exist on the wire with omitempty.
type MetricSeries struct {
	Name       string            `json:"name"`
	Unit       string            `json:"unit,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Datapoints []Datapoint       `json:"datapoints"`
}

// Datapoint is a single metric value at a point in time. Mirrors
// The InsideOut backend's Datapoint (aws_metrics.go:95). The field name "Average"
// is a historical accident — the CloudWatch Stat (Average / Sum /
// Maximum) is chosen per-metric in the spec table; this field carries
// whichever statistic was requested. Renaming is a downstream-API
// breaking change deferred indefinitely.
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
