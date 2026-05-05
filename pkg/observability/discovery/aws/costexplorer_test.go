// Mock-based unit tests for the Cost Explorer inspector.
//
// Ported from the InsideOut backend internal/agentapi/aws_billing_test.go (1-386).
// The InsideOut backend's integration tests (billingAWSConfig + the live-API tests
// after line 471) are intentionally NOT ported — they wrap the InsideOut backend's
// session/Oracle layer that the issue header for #225 explicitly excludes.

package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockCostExplorerClient struct {
	getCostAndUsageOutput    *costexplorer.GetCostAndUsageOutput
	getCostAndUsageErr       error
	getCostForecastOutput    *costexplorer.GetCostForecastOutput
	getCostForecastErr       error
	lastGetCostAndUsageInput *costexplorer.GetCostAndUsageInput
	lastGetCostForecastInput *costexplorer.GetCostForecastInput
}

func (m *mockCostExplorerClient) GetCostAndUsage(_ context.Context, params *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	m.lastGetCostAndUsageInput = params
	return m.getCostAndUsageOutput, m.getCostAndUsageErr
}

func (m *mockCostExplorerClient) GetCostForecast(_ context.Context, params *costexplorer.GetCostForecastInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostForecastOutput, error) {
	m.lastGetCostForecastInput = params
	return m.getCostForecastOutput, m.getCostForecastErr
}

func TestFormatUSD(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"zero", "0.000000", "$0.00"},
		{"whole dollars", "100.000000", "$100.00"},
		{"cents", "0.123456", "$0.12"},
		{"large amount", "12345.678900", "$12345.68"},
		{"negative", "-50.000000", "$-50.00"},
		{"small fraction", "0.001000", "$0.00"},
		{"invalid input", "not-a-number", "not-a-number"},
		{"empty string", "", ""},
		{"rounds up at 0.005", "0.005", "$0.01"},
		{"rounds down at 0.004", "0.004", "$0.00"},
		{"rounds up at 1.995", "1.995", "$2.00"},
		{"rounds down at 1.994", "1.994", "$1.99"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatUSD(tt.input))
		})
	}
}

func TestInspectCostExplorer_UnsupportedAction(t *testing.T) {
	t.Parallel()
	mock := &mockCostExplorerClient{}
	_, err := inspectCostExplorerWithDeps(context.Background(), mock, "nonexistent-action", "")
	require.Error(t, err)
	// unsupportedActionError formats as
	// `unsupported cost-explorer action: %q. Supported: %v`. Assert
	// substring matches both the verb and at least one canonical action
	// so a future helper rename surfaces the regression.
	assert.Contains(t, err.Error(), "unsupported cost-explorer action")
	assert.Contains(t, err.Error(), "get-cost-summary")
}

func TestInspectCostExplorer_GetCostByTag_MissingTagKey(t *testing.T) {
	t.Parallel()
	mock := &mockCostExplorerClient{}
	_, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-by-tag", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag_key")
}

func TestInspectCostExplorer_GetCostByTag_EmptyFilters(t *testing.T) {
	t.Parallel()
	mock := &mockCostExplorerClient{}
	_, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-by-tag", `{}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag_key")
}

func TestCostSummary_AggregationAndSorting(t *testing.T) {
	t.Parallel()

	mock := &mockCostExplorerClient{
		getCostAndUsageOutput: &costexplorer.GetCostAndUsageOutput{
			ResultsByTime: []cetypes.ResultByTime{
				{
					Groups: []cetypes.Group{
						{Keys: []string{"Amazon EC2"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("50.00")}}},
						{Keys: []string{"Amazon S3"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("10.00")}}},
						{Keys: []string{"AWS Lambda"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("5.00")}}},
					},
				},
				{
					Groups: []cetypes.Group{
						{Keys: []string{"Amazon EC2"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("60.00")}}},
						{Keys: []string{"Amazon S3"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("15.00")}}},
						{Keys: []string{"AWS Lambda"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("8.00")}}},
					},
				},
			},
		},
	}

	result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-summary", "")
	require.NoError(t, err)

	m := jsonRoundTrip(t, result)

	assert.Equal(t, "$148.00", m["total_cost"])
	assert.Equal(t, float64(3), m["service_count"])
	assert.Equal(t, "MONTHLY", m["granularity"])

	byService, ok := m["by_service"].([]any)
	require.True(t, ok, "by_service should be a slice")
	require.Len(t, byService, 3)

	first := byService[0].(map[string]any)
	assert.Equal(t, "Amazon EC2", first["service"])
	assert.Equal(t, "$110.00", first["cost"])

	second := byService[1].(map[string]any)
	assert.Equal(t, "Amazon S3", second["service"])
	assert.Equal(t, "$25.00", second["cost"])

	third := byService[2].(map[string]any)
	assert.Equal(t, "AWS Lambda", third["service"])
	assert.Equal(t, "$13.00", third["cost"])
}

func TestCostSummary_DataUnavailable(t *testing.T) {
	t.Parallel()

	mock := &mockCostExplorerClient{
		getCostAndUsageErr: fmt.Errorf("DataUnavailable: billing data not yet available"),
	}

	result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-summary", "")
	require.NoError(t, err, "DataUnavailable should be handled gracefully, not returned as error")

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, m["note"], "No billing data available")
	assert.Contains(t, m["period"], " to ")
}

func TestCostSummary_DaysClamping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filters  string
		wantDays int
	}{
		{"days=0 defaults to 30", `{"days":"0"}`, 30},
		{"days=500 clamped to 365", `{"days":"500"}`, 365},
		{"days=7 unchanged", `{"days":"7"}`, 7},
		{"negative defaults to 30", `{"days":"-5"}`, 30},
		{"no days defaults to 30", `{}`, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockCostExplorerClient{
				getCostAndUsageOutput: &costexplorer.GetCostAndUsageOutput{},
			}

			_, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-summary", tt.filters)
			require.NoError(t, err)

			require.NotNil(t, mock.lastGetCostAndUsageInput)
			start := aws.ToString(mock.lastGetCostAndUsageInput.TimePeriod.Start)
			end := aws.ToString(mock.lastGetCostAndUsageInput.TimePeriod.End)

			startTime, err := time.Parse("2006-01-02", start)
			require.NoError(t, err, "start date should be valid")
			endTime, err := time.Parse("2006-01-02", end)
			require.NoError(t, err, "end date should be valid")

			actualDays := int(endTime.Sub(startTime).Hours() / 24)
			assert.Equal(t, tt.wantDays, actualDays, "date range should span expected days")
		})
	}
}

func TestCostSummary_GranularityMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		filters         string
		wantGranularity cetypes.Granularity
	}{
		{"DAILY maps to GranularityDaily", `{"granularity":"DAILY"}`, cetypes.GranularityDaily},
		{"daily case insensitive", `{"granularity":"daily"}`, cetypes.GranularityDaily},
		{"empty defaults to MONTHLY", `{}`, cetypes.GranularityMonthly},
		{"no filters defaults to MONTHLY", "", cetypes.GranularityMonthly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockCostExplorerClient{
				getCostAndUsageOutput: &costexplorer.GetCostAndUsageOutput{},
			}

			_, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-summary", tt.filters)
			require.NoError(t, err)

			require.NotNil(t, mock.lastGetCostAndUsageInput)
			assert.Equal(t, tt.wantGranularity, mock.lastGetCostAndUsageInput.Granularity)
		})
	}
}

func TestCostSummary_EmptyResults(t *testing.T) {
	t.Parallel()

	mock := &mockCostExplorerClient{
		getCostAndUsageOutput: &costexplorer.GetCostAndUsageOutput{
			ResultsByTime: []cetypes.ResultByTime{},
		},
	}

	result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-summary", "")
	require.NoError(t, err)

	m := assertCostSummaryResult(t, result)
	assert.Equal(t, 0, m["service_count"])
	assert.Equal(t, "$0.00", m["total_cost"])
}

func TestCostForecast_HappyPath(t *testing.T) {
	t.Parallel()

	mock := &mockCostExplorerClient{
		getCostForecastOutput: &costexplorer.GetCostForecastOutput{
			Total: &cetypes.MetricValue{
				Amount: aws.String("250.50"),
				Unit:   aws.String("USD"),
			},
			ForecastResultsByTime: []cetypes.ForecastResult{
				{
					TimePeriod: &cetypes.DateInterval{
						Start: aws.String("2026-02-15"),
						End:   aws.String("2026-03-01"),
					},
					MeanValue: aws.String("250.50"),
				},
			},
		},
	}

	result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-forecast", "")
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)

	assert.Contains(t, m["period"], " to ")
	assert.Equal(t, "$250.50", m["forecast_total"])
	assert.Equal(t, "USD", m["unit"])

	periods, ok := m["by_period"].([]map[string]string)
	require.True(t, ok)
	require.Len(t, periods, 1)
	assert.Equal(t, "2026-02-15", periods[0]["start"])
	assert.Equal(t, "2026-03-01", periods[0]["end"])
	assert.Equal(t, "$250.50", periods[0]["mean_value"])
}

func TestCostForecast_InsufficientData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"DataUnavailable", fmt.Errorf("DataUnavailable: no historical data")},
		{"not enough data", fmt.Errorf("not enough data points available")},
		{"BillEstimateLineItemDataUnavailable", fmt.Errorf("BillEstimateLineItemDataUnavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockCostExplorerClient{
				getCostForecastErr: tt.err,
			}

			result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-forecast", "")
			require.NoError(t, err, "should handle gracefully")

			m, ok := result.(map[string]any)
			require.True(t, ok)
			assert.Contains(t, m["note"], "Cost forecast unavailable")
			assert.Contains(t, m["period"], " to ")
		})
	}
}

func TestCostForecast_RealError(t *testing.T) {
	t.Parallel()

	mock := &mockCostExplorerClient{
		getCostForecastErr: fmt.Errorf("AccessDeniedException: not authorized"),
	}

	_, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-forecast", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDeniedException")
}

func TestCostByTag_TagParsing(t *testing.T) {
	t.Parallel()

	mock := &mockCostExplorerClient{
		getCostAndUsageOutput: &costexplorer.GetCostAndUsageOutput{
			ResultsByTime: []cetypes.ResultByTime{
				{
					Groups: []cetypes.Group{
						{Keys: []string{"Environment$production"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("100.00")}}},
						{Keys: []string{"Environment$staging"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("30.00")}}},
						{Keys: []string{"Environment$"}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("5.00")}}},
						{Keys: []string{""}, Metrics: map[string]cetypes.MetricValue{"UnblendedCost": {Amount: aws.String("2.00")}}},
					},
				},
			},
		},
	}

	result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-by-tag", `{"tag_key":"Environment"}`)
	require.NoError(t, err)

	m := jsonRoundTrip(t, result)
	assert.Equal(t, "Environment", m["tag_key"])
	assert.Equal(t, "$137.00", m["total_cost"])
	assert.Contains(t, m["period"], " to ")

	byTag, ok := m["by_tag"].([]any)
	require.True(t, ok)
	require.Len(t, byTag, 3)

	first := byTag[0].(map[string]any)
	assert.Equal(t, "production", first["tag_value"])
	assert.Equal(t, "$100.00", first["cost"])

	second := byTag[1].(map[string]any)
	assert.Equal(t, "staging", second["tag_value"])
	assert.Equal(t, "$30.00", second["cost"])

	third := byTag[2].(map[string]any)
	assert.Equal(t, "(untagged)", third["tag_value"])
	assert.Equal(t, "$7.00", third["cost"])
}

func jsonRoundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// assertCostSummaryResult validates the full structural contract of a cost
// summary result. Returns the map for further assertions.
func assertCostSummaryResult(t *testing.T, result any) map[string]any {
	t.Helper()

	m, ok := result.(map[string]any)
	require.True(t, ok, "result should be map[string]any, got %T", result)

	period, ok := m["period"].(string)
	require.True(t, ok, "period must be a string")
	assert.Contains(t, period, " to ", "period should be 'start to end' format")

	if _, hasNote := m["note"]; hasNote {
		t.Logf("Cost API returned note: %v", m["note"])
		return m
	}

	totalCost, ok := m["total_cost"].(string)
	require.True(t, ok, "total_cost must be a string")
	assert.True(t, strings.HasPrefix(totalCost, "$"), "total_cost should start with $, got %q", totalCost)

	granularity, ok := m["granularity"].(string)
	require.True(t, ok, "granularity must be a string")
	assert.NotEmpty(t, granularity)

	var serviceCount int
	switch sc := m["service_count"].(type) {
	case int:
		serviceCount = sc
	case float64:
		serviceCount = int(sc)
	default:
		require.Fail(t, "service_count must be numeric", "got %T", m["service_count"])
	}
	assert.GreaterOrEqual(t, serviceCount, 0)

	byService := m["by_service"]
	if serviceCount > 0 {
		require.NotNil(t, byService, "by_service must not be nil when service_count > 0")
		data, err := json.Marshal(byService)
		require.NoError(t, err)
		var entries []map[string]any
		require.NoError(t, json.Unmarshal(data, &entries))
		require.Len(t, entries, serviceCount)
		for _, entry := range entries {
			svc, ok := entry["service"].(string)
			require.True(t, ok, "service must be a string")
			assert.NotEmpty(t, svc)
			cost, ok := entry["cost"].(string)
			require.True(t, ok, "cost must be a string")
			assert.True(t, strings.HasPrefix(cost, "$"), "cost should start with $, got %q", cost)
		}
	}

	return m
}

// --- Empty-state JSON-shape pins per #256 ---
//
// Cost Explorer wraps slices in parent maps, so the assertion shape is
// wrapped-parent (Pattern C in CONTRIBUTING.md): assert m["by_service"]
// / m["by_tag"] is non-nil and marshals to "[]". Pre-fix, declaring
// `sorted := []serviceCostEntry(nil)` would marshal as JSON null
// inside the parent envelope.

func TestCostSummary_NoResults_EmptySlice_ByService(t *testing.T) {
	t.Parallel()
	mock := &mockCostExplorerClient{
		getCostAndUsageOutput: &costexplorer.GetCostAndUsageOutput{
			ResultsByTime: []cetypes.ResultByTime{},
		},
	}
	result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-summary", "")
	require.NoError(t, err)

	m := assertCostSummaryResult(t, result)
	by := m["by_service"]
	require.NotNil(t, by, "by_service must be non-nil so JSON emits [] not null (#256)")

	b, err := json.Marshal(by)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cost Explorer get-cost-summary by_service must marshal as [] not null (#256)")
}

func TestCostByTag_NoResults_EmptySlice_ByTag(t *testing.T) {
	t.Parallel()
	mock := &mockCostExplorerClient{
		getCostAndUsageOutput: &costexplorer.GetCostAndUsageOutput{
			ResultsByTime: []cetypes.ResultByTime{},
		},
	}
	result, err := inspectCostExplorerWithDeps(context.Background(), mock, "get-cost-by-tag",
		`{"tag_key":"Project"}`)
	require.NoError(t, err)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	by := m["by_tag"]
	require.NotNil(t, by, "by_tag must be non-nil so JSON emits [] not null (#256)")

	b, err := json.Marshal(by)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cost Explorer get-cost-by-tag by_tag must marshal as [] not null (#256)")
}
