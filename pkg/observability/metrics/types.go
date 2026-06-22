// Package metrics is the CloudWatch / Cloud Monitoring metric-fetch
// wrapper ported from the InsideOut backend's metric paths. Per-service
// resource discovery (DescribeInstances, ListFunctions, etc.) is
// intentionally NOT included here — that surface lives in
// pkg/observability/discovery. This package handles the slice of the
// pipeline that runs after callers have already produced a list of
// resource IDs: build CloudWatch GetMetricDataQuery slices, hand them to
// the CloudWatch / Monitoring API, normalize the response into
// MetricsResult.
//
// The result/data TYPES (MetricsResult, ResourceMetrics, MetricSeries,
// Datapoint, MetricsFilter, ResourceID) moved into the SDK-free leaf
// package pkg/observability/metrics/metricstypes (reliable#2153) so a
// proxy consumer can deserialize a MetricsResult without importing the
// CloudWatch / Cloud Monitoring clients. The aliases below preserve the
// `metrics.MetricsResult` spelling for every existing in-tree caller —
// a Go type alias is identical to the aliased type, so signatures like
// Fetch(...) (MetricsResult, error) keep their exact shape.
package metrics

import "github.com/luthersystems/insideout-terraform-presets/pkg/observability/metrics/metricstypes"

// SDK-free result/data types, re-exported from metricstypes. See that
// package for the canonical definitions and JSON-tag contract.
type (
	// MetricsFilter controls the time range and granularity of metric queries.
	MetricsFilter = metricstypes.MetricsFilter
	// MetricsResult is the top-level response for a get-metrics action.
	MetricsResult = metricstypes.MetricsResult
	// ResourceMetrics holds metrics for a single resource.
	ResourceMetrics = metricstypes.ResourceMetrics
	// MetricSeries holds the data for a single metric.
	MetricSeries = metricstypes.MetricSeries
	// Datapoint is a single metric value at a point in time.
	Datapoint = metricstypes.Datapoint
	// ResourceID is a resource identifier for the metric-fetch path.
	ResourceID = metricstypes.ResourceID
)
