package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// Service-name constants for the GCP-side special cases the metric-fetch
// path has to know about. The "gcs" override mirrors reliable's
// getGCPServiceMetricsWithDeps GCS branch (gcp_metrics.go:411-417).
const (
	serviceGCS     = "gcs"
	serviceBastion = "bastion"
)

// GCS bucket-storage metrics need a daily aggregation period (Cloud
// Monitoring only publishes storage/total_bytes / object_count once a
// day). Mirrors the GCS override in reliable's
// getGCPServiceMetricsWithDeps (gcp_metrics.go:411-417). The 48h floor
// guarantees the chart renders at least two datapoints.
const (
	gcsPeriodSeconds = 86400 // 1 day
	gcsMinHours      = 48    // need >=2 datapoints in the window
)

// validAligners maps the string aligner names carried on
// observability.GCPMetricSpec to the proto enum values Cloud Monitoring
// expects on the Aggregation request. Mirrors reliable's validAligners
// (gcp_metrics.go:278). Specs carrying any other aligner are skipped
// with a logged warning — same defensive contract reliable holds.
var validAligners = map[string]monitoringpb.Aggregation_Aligner{
	"ALIGN_MEAN":          monitoringpb.Aggregation_ALIGN_MEAN,
	"ALIGN_RATE":          monitoringpb.Aggregation_ALIGN_RATE,
	"ALIGN_PERCENTILE_99": monitoringpb.Aggregation_ALIGN_PERCENTILE_99,
}

// FetchGCP is the public GCP metric-fetch entry point. It walks every
// metric in obs.Metrics, issues one ListTimeSeries call per metric (the
// Cloud Monitoring API is metric-scoped, not resource-scoped), groups
// the returned time-series rows by the resource label key declared on
// each spec, and assembles the result into a per-cloud-uniform
// MetricsResult.
//
// Public-API symmetry with the AWS Fetch (aws.go) is intentional:
// callers can switch between clouds by swapping obs and the clients
// argument. The resources slice is the one place the asymmetry leaks
// through:
//
//   - AWS Fetch issues one GetMetricData call per resource — it MUST
//     have the resource list.
//   - GCP FetchGCP issues one ListTimeSeries call per metric — Cloud
//     Monitoring returns every resource publishing the metric in the
//     project. resources is honored as a post-filter when non-empty
//     (caller wants to scope to a known inventory) and ignored when
//     empty (caller wants whatever the project surfaces). The empty-
//     slice contract matches reliable's getGCPServiceMetricsWithDeps,
//     which has no resource filter at all.
//
// service is the inspector-side join key (e.g. "compute", "cloudrun",
// "bastion"). It's used for two purposes:
//
//  1. Result.Service propagation, for downstream callers that key off
//     it.
//  2. service=="gcs" — daily metrics override; mf.Period to 86400 and
//     mf.Hours floor to >=48 so the chart has at least two datapoints.
//
// Note: bastion-alias resolution (bastion → compute) lives in reliable
// at gcp_metrics.go:399-401 because the catalog there is keyed by the
// post-resolution service. This package's authority join
// (componentObs/observability.Lookup) already resolves the alias before
// returning the GCPObs — KeyGCPBastion's Service is "bastion" but its
// metrics are the compute set. So the alias is invisible at this layer
// and we don't need to re-resolve it here.
//
// Per-metric ListTimeSeries failures log+skip rather than aborting the
// whole call — same partial-result contract as reliable
// (gcp_metrics.go:471-473) and the AWS Fetch (aws.go:138).
func FetchGCP(
	ctx context.Context,
	clients *GCPClients,
	service string,
	obs *observability.GCPObs,
	resources []ResourceID,
	mf MetricsFilter,
) (MetricsResult, error) {
	if clients == nil {
		return MetricsResult{}, fmt.Errorf("metrics: clients is required")
	}
	if clients.Monitoring == nil {
		return MetricsResult{}, fmt.Errorf("metrics: clients.Monitoring is required")
	}
	if clients.ProjectID == "" {
		return MetricsResult{}, fmt.Errorf("metrics: clients.ProjectID is required")
	}
	if obs == nil {
		return MetricsResult{}, fmt.Errorf("metrics: obs is required for service %q", service)
	}

	// Apply per-service overrides (mirrors gcp_metrics.go:411-417).
	period := mf.Period
	hours := mf.Hours
	if service == serviceGCS {
		period = gcsPeriodSeconds
		if hours < gcsMinHours {
			hours = gcsMinHours
		}
	}

	// resourceFilter is non-nil only when the caller explicitly scoped
	// to a list — otherwise we accept every resource the project
	// surfaces (matches reliable's no-filter contract).
	var resourceFilter map[string]bool
	if len(resources) > 0 {
		resourceFilter = make(map[string]bool, len(resources))
		for _, r := range resources {
			resourceFilter[r.ID] = true
		}
	}

	endTime := time.Now().UTC()
	startTime := endTime.Add(-time.Duration(hours) * time.Hour)

	// Two-level grouping: resourceID → metricKey → MetricSeries.
	// metricKey carries (name, serialized-labels) so a single metric
	// type with breakdown labels (e.g. cloudfunctions execution_count
	// per status=ok/error) yields one MetricSeries per label
	// combination — the breakdown surfaces as repeated entries with
	// the same Name. Mirrors reliable's resourceMetrics map
	// (gcp_metrics.go:424-428).
	resourceMetrics, metricErrors := fetchGCPSeries(ctx, clients, obs, period, startTime, endTime, resourceFilter)

	// Build sorted result for deterministic JSON output. Same sort
	// comparators reliable uses (gcp_metrics.go:534-550).
	resourceList := make([]ResourceMetrics, 0, len(resourceMetrics))
	for resID, metricsMap := range resourceMetrics {
		series := make([]MetricSeries, 0, len(metricsMap))
		for _, m := range metricsMap {
			series = append(series, *m)
		}
		sort.Slice(series, func(i, j int) bool {
			if series[i].Name != series[j].Name {
				return series[i].Name < series[j].Name
			}
			// Two entries with the same Name differ only by their
			// breakdown labels — serialize for a stable secondary
			// key, matching reliable's sort comparator
			// (gcp_metrics.go:539-541).
			li, _ := json.Marshal(series[i].Labels)
			lj, _ := json.Marshal(series[j].Labels)
			return string(li) < string(lj)
		})
		resourceList = append(resourceList, ResourceMetrics{
			ResourceID: resID,
			Metrics:    series,
		})
	}
	sort.Slice(resourceList, func(i, j int) bool {
		return resourceList[i].ResourceID < resourceList[j].ResourceID
	})

	if len(metricErrors) > 0 {
		// Aggregated warning preserves reliable's diagnostic surface
		// (gcp_metrics.go:559-561) without spamming one log line per
		// failed metric type.
		log.Printf("[metrics] partial failures for gcp/%s: %s", service, strings.Join(metricErrors, "; "))
	}

	return MetricsResult{
		Service:   service,
		TimeRange: fmt.Sprintf("last %d hours", hours),
		Period:    period,
		Resources: resourceList,
	}, nil
}

// metricSeriesKey identifies a unique (metric-name, label-set) bucket
// inside the per-resource grouping. Label-bearing breakdowns (e.g.
// status=ok vs status=error on cloudfunctions execution_count) need
// distinct entries; the serialized labels string keeps them apart
// without hashing or pointer comparison.
type metricSeriesKey struct {
	name   string
	labels string // JSON-serialized labels for dedup
}

// fetchGCPSeries is the core ListTimeSeries loop — extracted so the
// orchestration in FetchGCP stays under one screen and so an
// alternate shaper (e.g. cross-cloud aggregator in C16) can call it
// without repeating the request-building boilerplate.
func fetchGCPSeries(
	ctx context.Context,
	clients *GCPClients,
	obs *observability.GCPObs,
	period int,
	startTime, endTime time.Time,
	resourceFilter map[string]bool,
) (map[string]map[metricSeriesKey]*MetricSeries, []string) {
	resourceMetrics := make(map[string]map[metricSeriesKey]*MetricSeries)
	var metricErrors []string

	for _, spec := range obs.Metrics {
		filter := fmt.Sprintf(`metric.type = "%s" AND resource.type = "%s"`, spec.MetricType, spec.ResourceType)

		aligner, alignerOK := validAligners[spec.Aligner]
		if !alignerOK {
			metricErrors = append(metricErrors, fmt.Sprintf("invalid aligner %q for %s", spec.Aligner, spec.MetricType))
			continue
		}

		aggregation := &monitoringpb.Aggregation{
			AlignmentPeriod:    &durationpb.Duration{Seconds: int64(period)},
			PerSeriesAligner:   aligner,
			CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_NONE,
		}

		// GroupByFields drives the breakdown surface. For each label
		// in GroupByLabels we ask Cloud Monitoring to keep the metric
		// dimension AND the resource label key intact across the
		// reducer (which we flip to REDUCE_SUM so per-resource counts
		// sum across the value space we're not breaking down on).
		// Mirrors reliable's GroupByFields construction
		// (gcp_metrics.go:448-456).
		if len(spec.GroupByLabels) > 0 {
			for _, label := range spec.GroupByLabels {
				aggregation.GroupByFields = append(aggregation.GroupByFields,
					fmt.Sprintf("metric.label.%s", label),
					fmt.Sprintf("resource.label.%s", spec.LabelKey),
				)
			}
			aggregation.CrossSeriesReducer = monitoringpb.Aggregation_REDUCE_SUM
		}

		req := &monitoringpb.ListTimeSeriesRequest{
			Name:   fmt.Sprintf("projects/%s", clients.ProjectID),
			Filter: filter,
			Interval: &monitoringpb.TimeInterval{
				StartTime: timestamppb.New(startTime),
				EndTime:   timestamppb.New(endTime),
			},
			Aggregation: aggregation,
			View:        monitoringpb.ListTimeSeriesRequest_FULL,
		}

		series, err := clients.Monitoring.ListTimeSeries(ctx, req)
		if err != nil {
			log.Printf("[metrics] warning: ListTimeSeries failed for %s: %v", spec.MetricType, err)
			metricErrors = append(metricErrors, fmt.Sprintf("%s: %v", spec.DisplayName, err))
			continue
		}

		shapeGCPSeries(series, spec, resourceFilter, resourceMetrics)
	}

	return resourceMetrics, metricErrors
}

// shapeGCPSeries normalizes the proto TimeSeries response into the
// per-resource MetricSeries grouping. Extracted so the response-shape
// logic — which has its own nil-handling, dedup, and label-extraction
// quirks — can be unit-tested independently of the request loop.
//
// Mirrors reliable's per-spec inner loop (gcp_metrics.go:476-524).
func shapeGCPSeries(
	timeSeries []*monitoringpb.TimeSeries,
	spec observability.GCPMetricSpec,
	resourceFilter map[string]bool,
	resourceMetrics map[string]map[metricSeriesKey]*MetricSeries,
) {
	for _, ts := range timeSeries {
		// Resource ID extraction. The "unknown" fallback is a
		// reliable contract (gcp_metrics.go:482-484): time series
		// arriving with nil Resource or no LabelKey value still
		// surface in the result rather than being dropped — useful
		// for diagnosing aggregation misconfiguration.
		resID := ""
		if ts.Resource != nil && ts.Resource.Labels != nil {
			resID = ts.Resource.Labels[spec.LabelKey]
		}
		if resID == "" {
			resID = "unknown"
		}

		// Optional caller-side scope: only post-filter when the
		// caller explicitly handed us a resource list. The "unknown"
		// fallback is always allowed through so misconfigured
		// aggregations remain debuggable.
		if resourceFilter != nil && resID != "unknown" && !resourceFilter[resID] {
			continue
		}

		if resourceMetrics[resID] == nil {
			resourceMetrics[resID] = make(map[metricSeriesKey]*MetricSeries)
		}

		// Build labels map for breakdown metrics — only populated
		// when the spec carries GroupByLabels. The serialized labels
		// string is the dedup key so two time series for the same
		// (resource, metric) but different label values yield two
		// MetricSeries entries.
		var labels map[string]string
		var labelStr string
		if len(spec.GroupByLabels) > 0 && ts.Metric != nil && ts.Metric.Labels != nil {
			labels = make(map[string]string)
			for _, lbl := range spec.GroupByLabels {
				if v, ok := ts.Metric.Labels[lbl]; ok {
					labels[lbl] = v
				}
			}
			lblBytes, _ := json.Marshal(labels)
			labelStr = string(lblBytes)
		}

		key := metricSeriesKey{name: spec.DisplayName, labels: labelStr}
		entry, exists := resourceMetrics[resID][key]
		if !exists {
			entry = &MetricSeries{
				Name:   spec.DisplayName,
				Labels: labels, // nil for non-breakdown metrics; map otherwise.
			}
			resourceMetrics[resID][key] = entry
		}

		for _, pt := range ts.Points {
			if pt.Interval == nil || pt.Value == nil {
				continue
			}
			entry.Datapoints = append(entry.Datapoints, Datapoint{
				Timestamp: pt.Interval.EndTime.AsTime().Format(time.RFC3339),
				Average:   ExtractGCPValue(pt.Value),
			})
		}
	}
}

// ExtractGCPValue extracts a float64 from a Cloud Monitoring
// TypedValue. Mirrors reliable's extractGCPValue (gcp_metrics.go:567).
//
// Distribution values surface their Mean — Cloud Monitoring's
// distribution shape carries Count + BucketCounts as well, but the
// metric-watch chart only renders a single y-axis so the Mean is the
// honest summary. Bool values map true→1 and false→0 to fit the
// chart's float-only datapoint type.
//
// Exported so callers building bespoke shapers (e.g. the cross-cloud
// aggregator in C16) can reuse the same coercion semantics.
func ExtractGCPValue(v *monitoringpb.TypedValue) float64 {
	if v == nil {
		return 0
	}
	switch val := v.Value.(type) {
	case *monitoringpb.TypedValue_DoubleValue:
		return val.DoubleValue
	case *monitoringpb.TypedValue_Int64Value:
		return float64(val.Int64Value)
	case *monitoringpb.TypedValue_BoolValue:
		if val.BoolValue {
			return 1
		}
		return 0
	case *monitoringpb.TypedValue_DistributionValue:
		if val.DistributionValue != nil {
			return val.DistributionValue.Mean
		}
		return 0
	default:
		return 0
	}
}
