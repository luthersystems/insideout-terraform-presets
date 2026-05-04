package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	distributionpb "google.golang.org/genproto/googleapis/api/distribution"
	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// --- Mock Cloud Monitoring client ---

// fakeMonitoring captures every ListTimeSeries request the wrapper
// issues so tests can assert which filters / aggregation shapes hit
// the API. Mirrors the InsideOut backend's mockGCPMonitoringClient
// (gcp_metrics_test.go:28). Match keys are substrings of the metric
// type — the request filter carries `metric.type = "<full-type>"` so
// "cpu/utilization" or "execution_count" are unambiguous.
type fakeMonitoring struct {
	responses map[string][]*monitoringpb.TimeSeries
	errors    map[string]error
	calls     []*monitoringpb.ListTimeSeriesRequest
}

func (f *fakeMonitoring) ListTimeSeries(_ context.Context, req *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
	f.calls = append(f.calls, req)
	for key, err := range f.errors {
		if strings.Contains(req.Filter, key) {
			return nil, err
		}
	}
	for key, ts := range f.responses {
		if strings.Contains(req.Filter, key) {
			return ts, nil
		}
	}
	return nil, nil
}

// --- Helpers ---

// gcpClientsWithMon wires a fakeMonitoring into a GCPClients value so
// FetchGCP doesn't try to do real Application Default Credentials
// resolution. Skips NewGCPClients (which would fail in CI without ADC).
func gcpClientsWithMon(mon MonitoringAPI) *GCPClients {
	return &GCPClients{
		ProjectID:  "test-project",
		Monitoring: mon,
	}
}

// gcpSpec pulls the GCPObs out of the per-component authority for a
// given key. Mirrors awsSpec in aws_test.go.
func gcpSpec(t *testing.T, key composer.ComponentKey) *observability.GCPObs {
	t.Helper()
	o, ok := observability.Lookup(key)
	require.True(t, ok, "Lookup(%q) must return a record", key)
	require.NotNil(t, o.GCP, "%q must have a GCP spec", key)
	return o.GCP
}

func gcpSpecForService(t *testing.T, key composer.ComponentKey, wantService string) *observability.GCPObs {
	t.Helper()
	o, ok := observability.Lookup(key)
	require.True(t, ok)
	require.Equal(t, wantService, o.Service, "%q must map to service=%q", key, wantService)
	require.NotNil(t, o.GCP)
	return o.GCP
}

// --- ExtractGCPValue (from the InsideOut backend gcp_metrics_test.go:870) ---

func TestExtractGCPValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		value    *monitoringpb.TypedValue
		expected float64
	}{
		{
			"double",
			&monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 3.14}},
			3.14,
		},
		{
			"int64",
			&monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 42}},
			42,
		},
		{
			"bool true",
			&monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_BoolValue{BoolValue: true}},
			1,
		},
		{
			"bool false",
			&monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_BoolValue{BoolValue: false}},
			0,
		},
		{
			"distribution",
			&monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DistributionValue{
				DistributionValue: &distributionpb.Distribution{Mean: 99.5, Count: 10},
			}},
			99.5,
		},
		{
			"distribution nil inner",
			&monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DistributionValue{
				DistributionValue: nil,
			}},
			0,
		},
		{
			"nil value",
			nil,
			0,
		},
		{
			"empty TypedValue",
			&monitoringpb.TypedValue{},
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.expected, ExtractGCPValue(tt.value), 0.001)
		})
	}
}

// --- FetchGCP nil/empty guards ---

func TestFetchGCP_NilClients(t *testing.T) {
	t.Parallel()
	obs := gcpSpec(t, composer.KeyGCPCompute)
	_, err := FetchGCP(context.Background(), nil, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clients is required")
}

func TestFetchGCP_NilMonitoring(t *testing.T) {
	t.Parallel()
	obs := gcpSpec(t, composer.KeyGCPCompute)
	c := &GCPClients{ProjectID: "test-project"}
	_, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Monitoring is required")
}

func TestFetchGCP_EmptyProjectID(t *testing.T) {
	t.Parallel()
	obs := gcpSpec(t, composer.KeyGCPCompute)
	c := &GCPClients{Monitoring: &fakeMonitoring{}}
	_, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ProjectID is required")
}

func TestFetchGCP_NilObs(t *testing.T) {
	t.Parallel()
	c := gcpClientsWithMon(&fakeMonitoring{})
	_, err := FetchGCP(context.Background(), c, "compute", nil, nil, MetricsFilter{Hours: 6, Period: 300})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "obs is required")
}

// --- FetchGCP empty result shape ---

// TestFetchGCP_EmptyAPIResponse verifies the well-formed empty result
// when the API returns no time series for any metric. Mirrors
// The InsideOut backend's TestGetGCPServiceMetrics_EmptyResults
// (gcp_metrics_test.go:296).
func TestFetchGCP_EmptyAPIResponse(t *testing.T) {
	t.Parallel()
	mon := &fakeMonitoring{}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)
	assert.Equal(t, "compute", result.Service)
	assert.Equal(t, "last 6 hours", result.TimeRange)
	assert.Equal(t, 300, result.Period)
	assert.Empty(t, result.Resources)

	// One ListTimeSeries call per metric in the spec — the loop
	// always runs even when every response is empty.
	assert.Len(t, mon.calls, len(obs.Metrics))
}

// --- FetchGCP happy path ---

// TestFetchGCP_Compute_HappyPath mirrors the InsideOut backend's
// TestGetGCPServiceMetrics_Compute_HappyPath (gcp_metrics_test.go:312)
// — two metrics, two values on one, one value on the other, all on
// the same instance.
func TestFetchGCP_Compute_HappyPath(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	ts1 := timestamppb.New(now.Add(-10 * time.Minute))
	ts2 := timestamppb.New(now.Add(-5 * time.Minute))

	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-abc123", "zone": "us-central1-a"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: ts1}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.45}}},
						{Interval: &monitoringpb.TimeInterval{EndTime: ts2}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.67}}},
					},
				},
			},
			"disk/read_ops_count": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-abc123", "zone": "us-central1-a"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: ts1}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 100}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpecForService(t, composer.KeyGCPCompute, "compute")

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 12, Period: 600})
	require.NoError(t, err)

	assert.Equal(t, "compute", result.Service)
	assert.Equal(t, 600, result.Period)
	assert.Contains(t, result.TimeRange, "12")

	require.Len(t, result.Resources, 1)
	assert.Equal(t, "i-abc123", result.Resources[0].ResourceID)
	require.Len(t, result.Resources[0].Metrics, 2)

	var cpu *MetricSeries
	for i := range result.Resources[0].Metrics {
		if result.Resources[0].Metrics[i].Name == "CPU Utilization" {
			cpu = &result.Resources[0].Metrics[i]
		}
	}
	require.NotNil(t, cpu, "CPU Utilization metric must be present")
	require.Len(t, cpu.Datapoints, 2)
	assert.InDelta(t, 0.45, cpu.Datapoints[0].Average, 0.001)
	assert.InDelta(t, 0.67, cpu.Datapoints[1].Average, 0.001)

	// One ListTimeSeries call per metric in the spec.
	assert.Len(t, mon.calls, len(obs.Metrics))

	// Period-to-AlignmentPeriod propagation — every request should
	// carry the user-supplied 600s period.
	for _, call := range mon.calls {
		require.NotNil(t, call.Aggregation)
		assert.Equal(t, int64(600), call.Aggregation.AlignmentPeriod.Seconds)
	}
}

// --- FetchGCP per-service overrides ---

// TestFetchGCP_GCS_DailyOverride mirrors the InsideOut backend's
// TestGetGCPServiceMetrics_GCS_DailyOverride (gcp_metrics_test.go:381)
// — service=="gcs" forces Period=86400 and Hours floor to 48.
func TestFetchGCP_GCS_DailyOverride(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"storage/total_bytes": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gcs_bucket",
						Labels: map[string]string{"bucket_name": "my-bucket"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1048576}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpecForService(t, composer.KeyGCPGCS, "gcs")

	result, err := FetchGCP(context.Background(), c, "gcs", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	assert.Equal(t, 86400, result.Period, "GCS period must be 86400")
	assert.Contains(t, result.TimeRange, "48", "GCS hours must be bumped to at least 48")

	// AlignmentPeriod on every issued request must reflect the
	// override, not the caller-supplied 300s.
	for _, call := range mon.calls {
		require.NotNil(t, call.Aggregation)
		assert.Equal(t, int64(86400), call.Aggregation.AlignmentPeriod.Seconds)
	}

	require.Len(t, result.Resources, 1)
	assert.Equal(t, "my-bucket", result.Resources[0].ResourceID)
}

// TestFetchGCP_GCSDoesNotShortenLongerHours mirrors the AWS
// equivalent (TestFetch_S3DoesNotShortenLongerHours): Hours=72 must
// NOT be clamped down to 48; the floor is one-sided.
func TestFetchGCP_GCSDoesNotShortenLongerHours(t *testing.T) {
	t.Parallel()
	mon := &fakeMonitoring{}
	c := gcpClientsWithMon(mon)
	obs := gcpSpecForService(t, composer.KeyGCPGCS, "gcs")

	result, err := FetchGCP(context.Background(), c, "gcs", obs, nil, MetricsFilter{Hours: 72, Period: 300})
	require.NoError(t, err)
	assert.Contains(t, result.TimeRange, "72")
	assert.Equal(t, 86400, result.Period)
}

// --- FetchGCP partial failures ---

// TestFetchGCP_PartialFailure mirrors the InsideOut backend's
// TestGetGCPServiceMetrics_PartialFailure (gcp_metrics_test.go:421).
// One metric returns a permission-denied error; the other still
// surfaces its data and the overall call does NOT bubble up the
// error.
func TestFetchGCP_PartialFailure(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-abc123"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.5}}},
					},
				},
			},
		},
		errors: map[string]error{
			"disk/read_ops_count": errors.New("permission denied"),
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err, "partial failures must not bubble up")

	require.Len(t, result.Resources, 1)
	assert.Equal(t, "i-abc123", result.Resources[0].ResourceID)
	require.Len(t, result.Resources[0].Metrics, 1)
	assert.Equal(t, "CPU Utilization", result.Resources[0].Metrics[0].Name)
	require.Len(t, result.Resources[0].Metrics[0].Datapoints, 1)
	assert.InDelta(t, 0.5, result.Resources[0].Metrics[0].Datapoints[0].Average, 0.001)
}

// --- Label breakdown ---

// TestFetchGCP_CloudFunctions_LabelBreakdown mirrors the InsideOut backend's
// TestGetGCPServiceMetrics_CloudFunctions_LabelBreakdown
// (gcp_metrics_test.go:463). Two time series for the same function
// with different status labels become two MetricSeries entries with
// matching Name and distinct Labels.
func TestFetchGCP_CloudFunctions_LabelBreakdown(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"execution_count": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "cloud_function",
						Labels: map[string]string{"function_name": "my-func"},
					},
					Metric: &metricpb.Metric{
						Type:   "cloudfunctions.googleapis.com/function/execution_count",
						Labels: map[string]string{"status": "ok"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 100}}},
					},
				},
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "cloud_function",
						Labels: map[string]string{"function_name": "my-func"},
					},
					Metric: &metricpb.Metric{
						Type:   "cloudfunctions.googleapis.com/function/execution_count",
						Labels: map[string]string{"status": "error"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 5}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpecForService(t, composer.KeyGCPCloudFunctions, "cloudfunctions")

	result, err := FetchGCP(context.Background(), c, "cloudfunctions", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	require.Len(t, result.Resources, 1)
	assert.Equal(t, "my-func", result.Resources[0].ResourceID)

	var execMetrics []MetricSeries
	for _, m := range result.Resources[0].Metrics {
		if m.Name == "Execution Count (Gen1)" {
			execMetrics = append(execMetrics, m)
		}
	}
	require.Len(t, execMetrics, 2, "two label-breakdown rows expected")

	values := make(map[string]float64)
	for _, m := range execMetrics {
		require.NotEmpty(t, m.Labels, "breakdown rows must carry Labels")
		status := m.Labels["status"]
		require.NotEmpty(t, status)
		require.Len(t, m.Datapoints, 1)
		values[status] = m.Datapoints[0].Average
	}
	assert.InDelta(t, 100, values["ok"], 0.001)
	assert.InDelta(t, 5, values["error"], 0.001)
}

// TestFetchGCP_CloudFunctions_GroupByFieldsInRequest mirrors
// The InsideOut backend's TestGetGCPServiceMetrics_CloudFunctions_GroupByFieldsInRequest
// (gcp_metrics_test.go:725) — verifies the Aggregation request
// carries GroupByFields when the spec has GroupByLabels.
func TestFetchGCP_CloudFunctions_GroupByFieldsInRequest(t *testing.T) {
	t.Parallel()
	mon := &fakeMonitoring{}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCloudFunctions)

	_, err := FetchGCP(context.Background(), c, "cloudfunctions", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	var foundExecution bool
	for _, call := range mon.calls {
		if strings.Contains(call.Filter, "execution_count") {
			require.NotNil(t, call.Aggregation)
			assert.Contains(t, call.Aggregation.GroupByFields, "metric.label.status")
			assert.Equal(t, monitoringpb.Aggregation_REDUCE_SUM, call.Aggregation.CrossSeriesReducer)
			foundExecution = true
		}
	}
	assert.True(t, foundExecution, "expected an execution_count request")
}

// --- Mixed resource types ---

// TestFetchGCP_VPC_MixedResources mirrors the InsideOut backend's
// TestGetGCPServiceMetrics_VPC_MixedResources (gcp_metrics_test.go:536).
// VPC mixes firewall metrics on gce_instance (instance_id) with NAT
// metrics on nat_gateway (gateway_name) — they end up as two
// distinct ResourceMetrics entries.
func TestFetchGCP_VPC_MixedResources(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"firewall/dropped_packets_count": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-instance1"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 42}}},
					},
				},
			},
			"nat/sent_bytes_count": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "nat_gateway",
						Labels: map[string]string{"gateway_name": "my-nat-gw"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 8192}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpecForService(t, composer.KeyGCPVPC, "vpc")

	result, err := FetchGCP(context.Background(), c, "vpc", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	require.Len(t, result.Resources, 2)
	resMap := make(map[string][]MetricSeries)
	for _, r := range result.Resources {
		resMap[r.ResourceID] = r.Metrics
	}

	instanceMetrics, ok := resMap["i-instance1"]
	require.True(t, ok)
	require.Len(t, instanceMetrics, 1)
	assert.Equal(t, "Firewall Dropped Packets", instanceMetrics[0].Name)
	assert.InDelta(t, 42, instanceMetrics[0].Datapoints[0].Average, 0.001)

	natMetrics, ok := resMap["my-nat-gw"]
	require.True(t, ok)
	require.Len(t, natMetrics, 1)
	assert.Equal(t, "NAT Sent Bytes", natMetrics[0].Name)
	assert.InDelta(t, 8192, natMetrics[0].Datapoints[0].Average, 0.001)
}

// --- Bastion alias is NOT resolved here (callers pass the resolved spec) ---

// TestFetchGCP_AcceptsAnyServiceLabelWithComputeSpec documents the
// intentional split with the InsideOut backend: the InsideOut backend's getGCPServiceMetricsWithDeps
// resolved the bastion→compute alias inline (gcp_metrics.go:399-401)
// because the catalog lived in the wrapper file. The local design
// keeps the catalog in the authority layer (component_observability.go)
// and lets FetchGCP take whatever GCPObs the caller hands it — so the
// bastion alias must already be applied upstream. To prove the wrapper
// is service-name-agnostic at this layer, we pass service="bastion"
// with the compute spec and verify it works.
func TestFetchGCP_AcceptsAnyServiceLabelWithComputeSpec(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "bastion-vm"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.1}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)

	computeObs := gcpSpec(t, composer.KeyGCPCompute)
	result, err := FetchGCP(context.Background(), c, "bastion", computeObs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	// Service label echoed verbatim — wrapper does not rewrite it.
	assert.Equal(t, "bastion", result.Service)
	require.Len(t, result.Resources, 1)
	assert.Equal(t, "bastion-vm", result.Resources[0].ResourceID)
	// One call per compute metric — confirms the spec really has the
	// compute set under the hood.
	assert.Len(t, mon.calls, len(computeObs.Metrics))
}

// --- "unknown" fallback ---

// TestFetchGCP_NilResourceFallsBackToUnknown mirrors the InsideOut backend's
// TestGetGCPServiceMetrics_NilResourceFallsBackToUnknown
// (gcp_metrics_test.go:638). Time series with nil Resource (or
// missing LabelKey value) end up under "unknown" rather than being
// dropped — useful for diagnosing aggregation misconfiguration.
func TestFetchGCP_NilResourceFallsBackToUnknown(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: nil, // nil Resource
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.3}}},
					},
				},
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"zone": "us-central1-a"}, // no instance_id
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.4}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	require.Len(t, result.Resources, 1)
	assert.Equal(t, "unknown", result.Resources[0].ResourceID)
}

// TestFetchGCP_NilIntervalAndValueSkipped mirrors the InsideOut backend's
// TestGetGCPServiceMetrics_NilIntervalAndValueSkipped
// (gcp_metrics_test.go:679). Datapoints with nil Interval or nil
// Value are skipped rather than panicking.
func TestFetchGCP_NilIntervalAndValueSkipped(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-test"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.5}}},
						{Interval: nil, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.9}}},
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: nil},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	require.Len(t, result.Resources, 1)
	var cpu *MetricSeries
	for i := range result.Resources[0].Metrics {
		if result.Resources[0].Metrics[i].Name == "CPU Utilization" {
			cpu = &result.Resources[0].Metrics[i]
		}
	}
	require.NotNil(t, cpu)
	require.Len(t, cpu.Datapoints, 1, "only the valid datapoint should survive")
	assert.InDelta(t, 0.5, cpu.Datapoints[0].Average, 0.001)
}

// --- Resource filter (local extension; not in the InsideOut backend) ---

// TestFetchGCP_ResourceFilter exercises the post-filter path that
// scopes results to a caller-supplied resource list. The InsideOut backend's GCP
// wrapper has no such filter (it returns whatever ListTimeSeries
// surfaces); the local API surfaces it for symmetry with AWS Fetch.
// The "unknown" fallback is always allowed through so misconfigured
// aggregations remain debuggable.
func TestFetchGCP_ResourceFilter(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-keep"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.5}}},
					},
				},
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-drop"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.9}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs,
		[]ResourceID{{ID: "i-keep", DimensionName: "instance_id"}},
		MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	require.Len(t, result.Resources, 1, "filter should drop i-drop")
	assert.Equal(t, "i-keep", result.Resources[0].ResourceID)
}

// TestFetchGCP_ResourceFilter_UnknownPassesThrough verifies that
// "unknown" is always preserved even when a resource filter is
// active. Misconfigured aggregations (nil Resource on a TimeSeries)
// must remain visible for debugging.
func TestFetchGCP_ResourceFilter_UnknownPassesThrough(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: nil,
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.3}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs,
		[]ResourceID{{ID: "i-anything"}},
		MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	require.Len(t, result.Resources, 1)
	assert.Equal(t, "unknown", result.Resources[0].ResourceID)
}

// --- Filter / interval contract on the wire ---

// TestFetchGCP_RequestShape pins the wire-shape of the
// ListTimeSeriesRequest the wrapper builds: project parent path,
// metric+resource type filter, interval bounds, view=FULL.
// Drift in any of these silently breaks the metric panel.
func TestFetchGCP_RequestShape(t *testing.T) {
	t.Parallel()
	mon := &fakeMonitoring{}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	startBefore := time.Now().UTC()
	_, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 3, Period: 120})
	require.NoError(t, err)
	endAfter := time.Now().UTC()

	require.NotEmpty(t, mon.calls)
	for _, call := range mon.calls {
		assert.Equal(t, "projects/test-project", call.Name, "Name should be the project parent path")

		// Filter must constrain on both metric.type and resource.type.
		assert.Contains(t, call.Filter, `metric.type = "compute.googleapis.com/instance/`)
		assert.Contains(t, call.Filter, `resource.type = "gce_instance"`)

		// Interval bounds must straddle the call timestamp.
		require.NotNil(t, call.Interval)
		require.NotNil(t, call.Interval.StartTime)
		require.NotNil(t, call.Interval.EndTime)
		actualStart := call.Interval.StartTime.AsTime()
		actualEnd := call.Interval.EndTime.AsTime()
		// Start should be ~3h before End.
		gap := actualEnd.Sub(actualStart)
		assert.InDelta(t, 3*time.Hour, gap, float64(time.Second))
		// Bounds should be inside the test wall-clock window.
		assert.True(t, !actualStart.Before(startBefore.Add(-3*time.Hour-time.Second)))
		assert.True(t, !actualEnd.After(endAfter.Add(time.Second)))

		assert.Equal(t, monitoringpb.ListTimeSeriesRequest_FULL, call.View)
	}
}

// --- Invalid aligner is skipped, not fatal ---

// TestFetchGCP_InvalidAlignerSkipped ensures a spec carrying an
// unknown aligner string doesn't blow up the whole call — the metric
// is dropped with a logged warning, every other metric still ships.
// The defensive contract matches the InsideOut backend's
// gcp_metrics.go:435-439.
func TestFetchGCP_InvalidAlignerSkipped(t *testing.T) {
	t.Parallel()
	mon := &fakeMonitoring{}
	c := gcpClientsWithMon(mon)

	obs := &observability.GCPObs{
		Metrics: []observability.GCPMetricSpec{
			{MetricType: "compute.googleapis.com/instance/cpu/utilization", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_BOGUS", DisplayName: "Bogus"},
			{MetricType: "compute.googleapis.com/instance/disk/read_ops_count", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_RATE", DisplayName: "Disk Read Ops"},
		},
	}

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)
	// Empty result (no matching responses) but the call was issued for
	// the valid metric only — invalid aligners short-circuit before
	// the API call.
	assert.Empty(t, result.Resources)
	assert.Len(t, mon.calls, 1, "only the valid-aligner metric should issue a request")
	assert.Contains(t, mon.calls[0].Filter, "disk/read_ops_count")
}

// --- TypedValue coercions ---

// TestFetchGCP_Int64Coercion checks that Int64 values round-trip
// through ExtractGCPValue + Datapoint.Average without precision loss
// in the chart range.
func TestFetchGCP_Int64Coercion(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "i-int64"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1234567890}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)
	require.Len(t, result.Resources, 1)

	var cpu *MetricSeries
	for i := range result.Resources[0].Metrics {
		if result.Resources[0].Metrics[i].Name == "CPU Utilization" {
			cpu = &result.Resources[0].Metrics[i]
		}
	}
	require.NotNil(t, cpu)
	require.Len(t, cpu.Datapoints, 1)
	assert.InDelta(t, 1234567890, cpu.Datapoints[0].Average, 0.001)
}

// TestFetchGCP_DistributionCoercion verifies a Distribution value
// surfaces its Mean. Cloud SQL p99 latencies and similar are typical
// distribution-typed metrics.
func TestFetchGCP_DistributionCoercion(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"request_latencies": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "cloud_run_revision",
						Labels: map[string]string{"service_name": "my-svc"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DistributionValue{
							DistributionValue: &distributionpb.Distribution{Mean: 250.5, Count: 100},
						}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpecForService(t, composer.KeyGCPCloudRun, "cloudrun")

	result, err := FetchGCP(context.Background(), c, "cloudrun", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)
	require.Len(t, result.Resources, 1)

	var latency *MetricSeries
	for i := range result.Resources[0].Metrics {
		if result.Resources[0].Metrics[i].Name == "Request Latency (p99)" {
			latency = &result.Resources[0].Metrics[i]
		}
	}
	require.NotNil(t, latency, "Request Latency (p99) must surface")
	require.Len(t, latency.Datapoints, 1)
	assert.InDelta(t, 250.5, latency.Datapoints[0].Average, 0.001)
}

// --- Sort determinism ---

// TestFetchGCP_DeterministicResourceOrder pins the lexicographic
// sort on ResourceID so the JSON output is stable across runs.
func TestFetchGCP_DeterministicResourceOrder(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mon := &fakeMonitoring{
		responses: map[string][]*monitoringpb.TimeSeries{
			"cpu/utilization": {
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "z-last"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.1}}},
					},
				},
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "a-first"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.2}}},
					},
				},
				{
					Resource: &monitoredrespb.MonitoredResource{
						Type:   "gce_instance",
						Labels: map[string]string{"instance_id": "m-middle"},
					},
					Points: []*monitoringpb.Point{
						{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(now)}, Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0.3}}},
					},
				},
			},
		},
	}
	c := gcpClientsWithMon(mon)
	obs := gcpSpec(t, composer.KeyGCPCompute)

	result, err := FetchGCP(context.Background(), c, "compute", obs, nil, MetricsFilter{Hours: 6, Period: 300})
	require.NoError(t, err)

	require.Len(t, result.Resources, 3)
	assert.Equal(t, "a-first", result.Resources[0].ResourceID)
	assert.Equal(t, "m-middle", result.Resources[1].ResourceID)
	assert.Equal(t, "z-last", result.Resources[2].ResourceID)
}

// --- BuildGetMetricDataQueries equivalent: spec coverage smoke test ---

// TestFetchGCP_AllAuthoritySpecsAreUsable walks every GCP service
// available via the authority layer and confirms FetchGCP can
// process its spec without panicking on zero responses. Catches any
// regression where an authority entry adds a metric with an
// untranslatable aligner or an empty MetricType.
func TestFetchGCP_AllAuthoritySpecsAreUsable(t *testing.T) {
	t.Parallel()
	for _, key := range composer.AllComponentKeys {
		o, ok := observability.Lookup(key)
		if !ok || o.GCP == nil || len(o.GCP.Metrics) == 0 {
			continue
		}
		k := key
		obs := o
		t.Run(string(k), func(t *testing.T) {
			t.Parallel()
			// Each subtest gets its own fakeMonitoring — fakeMonitoring
			// mutates `calls` without a lock, so a shared instance plus
			// t.Parallel() trips the race detector.
			c := gcpClientsWithMon(&fakeMonitoring{})
			result, err := FetchGCP(context.Background(), c, obs.Service, obs.GCP, nil, MetricsFilter{Hours: 6, Period: 300})
			require.NoError(t, err)
			assert.Equal(t, obs.Service, result.Service)
		})
	}
}

// TestEveryGCPSpec_PinsMetricTypesAndDisplayNames pins the (MetricType,
// DisplayName) ordered pair of every metric in every GCP service's
// catalog spec. Imported from the InsideOut backend's deleted
// `internal/agentapi/gcp_metrics_test.go::TestGCPMetricDefinitions_
// SpecificValues` — the upstream consolidation (the InsideOut backend#1252 / #1266)
// removed it, but its assertions are load-bearing and have no
// equivalent upstream.
//
// What this catches that nothing else does:
//
//   - Metric-path typos (e.g. swapping topic_id ↔ subscription_id in
//     pubsub paths, or fat-fingering the namespace).
//   - Cross-service metric-namespace pivots: cloudkms / cloudbuild /
//     identityplatform all pull from `serviceruntime.googleapis.com/
//     api/request_count` and pin DIFFERENT DisplayNames per service —
//     a future "cleanup" that collapses them to one DisplayName would
//     silently break the panel legends across three components.
//   - User-visible display-name drift. Cloudfunctions' "(Gen1)" /
//     "(Gen1, p99)" suffixes were added in the InsideOut backend#1143 to
//     disambiguate from cloudrun (Gen2 metrics under cloud_run_revision).
//     Drop them and the chart legend silently lies.
//   - Resource-type / metric-type mismatches that PR #238 already
//     corrected once (firestore document/{read,write,delete}_count
//     publishing only under firestore_instance, not Database).
//     Pinning the modern *_ops_count names traps re-regression.
//
// Order matters: the InsideOut backend's UI legends iterate in spec order. A re-
// arrangement that doesn't change the set still changes the user
// experience, so the test asserts ordered equality, not set equality.
//
// The test pins **all 17** GCP services with non-empty Metrics —
// not the 13 in the original the InsideOut backend test (the catalog has grown by
// 4 services since: apigateway, cloudbuild, cloudcdn, identityplatform,
// vertexai, cloudarmor were added or expanded). The "EveryGCPSpec"
// name is intentional — when a future contributor adds a new GCP
// service to the catalog, the drift trap below forces them to add an
// expectation here too.
func TestEveryGCPSpec_PinsMetricTypesAndDisplayNames(t *testing.T) {
	t.Parallel()

	type pin struct {
		metricType  string
		displayName string
	}

	expected := map[composer.ComponentKey][]pin{
		composer.KeyGCPCompute: {
			{"compute.googleapis.com/instance/cpu/utilization", "CPU Utilization"},
			{"compute.googleapis.com/instance/disk/read_ops_count", "Disk Read Ops"},
			{"compute.googleapis.com/instance/disk/write_ops_count", "Disk Write Ops"},
			{"compute.googleapis.com/instance/network/received_bytes_count", "Network Received Bytes"},
			{"compute.googleapis.com/instance/network/sent_bytes_count", "Network Sent Bytes"},
		},
		composer.KeyGCPCloudRun: {
			{"run.googleapis.com/request_count", "Request Count"},
			{"run.googleapis.com/container/instance_count", "Instance Count"},
			{"run.googleapis.com/request_latencies", "Request Latency (p99)"},
		},
		composer.KeyGCPCloudFunctions: {
			// (Gen1) suffix disambiguates from cloudrun (Gen2). Drop
			// it and the panel legend silently lies — the InsideOut backend#1143.
			{"cloudfunctions.googleapis.com/function/execution_count", "Execution Count (Gen1)"},
			{"cloudfunctions.googleapis.com/function/execution_times", "Execution Time (Gen1, p99)"},
		},
		composer.KeyGCPLoadbalancer: {
			{"loadbalancing.googleapis.com/https/request_count", "Request Count"},
			{"loadbalancing.googleapis.com/https/backend_latencies", "Backend Latency (p99)"},
		},
		composer.KeyGCPAPIGateway: {
			{"apigateway.googleapis.com/gateway/request_count", "Request Count"},
			{"apigateway.googleapis.com/gateway/latencies", "Latency (p99)"},
		},
		composer.KeyGCPGCS: {
			{"storage.googleapis.com/storage/total_bytes", "Total Bytes"},
			{"storage.googleapis.com/storage/object_count", "Object Count"},
			{"storage.googleapis.com/api/request_count", "API Request Count"},
		},
		composer.KeyGCPCloudSQL: {
			{"cloudsql.googleapis.com/database/cpu/utilization", "CPU Utilization"},
			{"cloudsql.googleapis.com/database/memory/utilization", "Memory Utilization"},
			{"cloudsql.googleapis.com/database/disk/utilization", "Disk Utilization"},
			{"cloudsql.googleapis.com/database/network/connections", "Connections"},
		},
		composer.KeyGCPVPC: {
			{"compute.googleapis.com/firewall/dropped_packets_count", "Firewall Dropped Packets"},
			{"router.googleapis.com/nat/sent_bytes_count", "NAT Sent Bytes"},
		},
		composer.KeyGCPCloudKMS: {
			// KMS metrics live under serviceruntime, NOT
			// cloudkms.googleapis.com — easy to fat-finger and
			// silently break Key Request Count panels.
			{"serviceruntime.googleapis.com/api/request_count", "Key Request Count"},
		},
		composer.KeyGCPPubSub: {
			{"pubsub.googleapis.com/topic/send_message_operation_count", "Topic Send Message Count"},
			{"pubsub.googleapis.com/subscription/num_undelivered_messages", "Subscription Backlog"},
			{"pubsub.googleapis.com/subscription/oldest_unacked_message_age", "Oldest Unacked Message Age"},
		},
		composer.KeyGCPFirestore: {
			// All four under resource.type
			// firestore.googleapis.com/Database (per-database
			// scoping). The legacy *_count variants only publish
			// under firestore_instance — re-introducing them here
			// silently regresses PR #238's catalog correction.
			{"firestore.googleapis.com/api/request_latencies", "API Request Latency (p99)"},
			{"firestore.googleapis.com/document/read_ops_count", "Document Read Ops"},
			{"firestore.googleapis.com/document/write_ops_count", "Document Write Ops"},
			{"firestore.googleapis.com/document/delete_ops_count", "Document Delete Ops"},
		},
		composer.KeyGCPCloudArmor: {
			{"networksecurity.googleapis.com/https/request_count", "Cloud Armor Requests"},
		},
		composer.KeyGCPMemorystore: {
			{"redis.googleapis.com/stats/memory/usage_ratio", "Memory Usage Ratio"},
			{"redis.googleapis.com/clients/connected", "Connected Clients"},
			{"redis.googleapis.com/stats/cpu_utilization", "CPU Utilization"},
		},
		composer.KeyGCPCloudBuild: {
			// Same MetricType as cloudkms + identityplatform; the
			// per-service DisplayName is the disambiguator, so a
			// "cleanup" that collapses them to one string would
			// break three panels at once.
			{"serviceruntime.googleapis.com/api/request_count", "Cloud Build API Requests"},
		},
		composer.KeyGCPIdentityPlatform: {
			{"serviceruntime.googleapis.com/api/request_count", "Identity Platform API Requests"},
		},
		composer.KeyGCPVertexAI: {
			{"aiplatform.googleapis.com/prediction/online/prediction_count", "Online Prediction Count"},
			{"aiplatform.googleapis.com/prediction/online/error_count", "Online Prediction Errors"},
			{"aiplatform.googleapis.com/prediction/online/prediction_latencies", "Online Prediction Latency (p99)"},
		},
	}

	// Per-service ordered pin assertions.
	for key, want := range expected {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			spec := gcpSpec(t, key)
			require.Len(t, spec.Metrics, len(want),
				"%s: catalog metric count drift — want %d, got %d (panel legend will gain or drop entries)", key, len(want), len(spec.Metrics))
			for i, m := range spec.Metrics {
				assert.Equal(t, want[i].metricType, m.MetricType,
					"%s metric[%d]: MetricType drift — request will hit a path the API doesn't publish", key, i)
				assert.Equal(t, want[i].displayName, m.DisplayName,
					"%s metric[%d]: DisplayName drift — the UI legend will silently show %q instead", key, i, m.DisplayName)
			}
		})
	}

	// Drift trap: every catalog key with non-empty Metrics must have an
	// expectation. Catches the "added a service to the catalog, forgot
	// to pin it here" case — without this, new metrics drift silently.
	t.Run("DriftTrap_EveryGCPSpecCovered", func(t *testing.T) {
		t.Parallel()
		for _, key := range composer.AllComponentKeys {
			o, ok := observability.Lookup(key)
			if !ok || o.GCP == nil || len(o.GCP.Metrics) == 0 {
				continue
			}
			if _, pinned := expected[key]; !pinned {
				t.Errorf("key %q has %d GCP metrics in the catalog but no pin in TestEveryGCPSpec_PinsMetricTypesAndDisplayNames — add an expected entry so panel-legend drift trips this test",
					key, len(o.GCP.Metrics))
			}
		}
	})
}
