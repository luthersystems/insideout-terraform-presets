package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// --- Mock CloudWatch client ---

// fakeCloudWatch captures the last GetMetricData input so tests can
// assert which client got called (CloudFront → us-east-1 vs default).
// Mirrors reliable's mockCloudWatchClient (aws_metrics_test.go:72).
type fakeCloudWatch struct {
	output    *cloudwatch.GetMetricDataOutput
	err       error
	lastInput *cloudwatch.GetMetricDataInput
	calls     int
}

func (m *fakeCloudWatch) GetMetricData(_ context.Context, params *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	m.calls++
	m.lastInput = params
	if m.err != nil {
		return nil, m.err
	}
	if m.output == nil {
		return &cloudwatch.GetMetricDataOutput{}, nil
	}
	return m.output, nil
}

// clientsWithCW wires a fakeCloudWatch into a Clients value so the
// Fetch path doesn't try to do real AWS auth. Skips NewClients (which
// would fail in CI without ambient creds).
func clientsWithCW(cw CloudWatchAPI) *Clients {
	return &Clients{
		Region:     "eu-west-2",
		CloudWatch: cw,
	}
}

// clientsWithCFOverride pre-populates the lazy CloudFront client field
// so Fetch routes to it without trying to call config.LoadDefaultConfig.
func clientsWithCFOverride(defaultCW, cfCW CloudWatchAPI) *Clients {
	return &Clients{
		Region:       "eu-west-2",
		CloudWatch:   defaultCW,
		cloudFrontCW: cfCW,
	}
}

// --- Spec helper: pull AWSObs out of the per-component authority. ---

func awsSpec(t *testing.T, key composer.ComponentKey) *observability.AWSObs {
	t.Helper()
	o, ok := observability.Lookup(key)
	require.True(t, ok, "Lookup(%q) must return a record", key)
	require.NotNil(t, o.AWS, "%q must have an AWS spec", key)
	return o.AWS
}

func awsSpecForService(t *testing.T, key composer.ComponentKey, wantService string) *observability.AWSObs {
	t.Helper()
	o, ok := observability.Lookup(key)
	require.True(t, ok)
	require.Equal(t, wantService, o.Service, "%q must map to service=%q", key, wantService)
	require.NotNil(t, o.AWS)
	return o.AWS
}

// --- ParseMetricsFilter (from reliable aws_metrics_test.go:163) ---

func TestParseMetricsFilter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		input          string
		expectedHours  int
		expectedPeriod int
	}{
		{"empty string", "", 6, 300},
		{"valid json", `{"hours":12,"period":600}`, 12, 600},
		{"partial json hours only", `{"hours":24}`, 24, 300},
		{"partial json period only", `{"period":60}`, 6, 60},
		{"zero hours defaults", `{"hours":0}`, 6, 300},
		{"negative period defaults", `{"period":-1}`, 6, 300},
		{"invalid json", `not json`, 6, 300},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := ParseMetricsFilter(tt.input)
			assert.Equal(t, tt.expectedHours, f.Hours)
			assert.Equal(t, tt.expectedPeriod, f.Period)
		})
	}
}

// --- BuildGetMetricDataQueries (from reliable aws_metrics_test.go:192) ---

// TestBuildGetMetricDataQueries_VerifiesDimensionValues walks every
// service spec exposed via observability.Lookup and confirms each
// query carries the spec's namespace + dimension name and the
// per-resource ID. Catches drift between observability.AWSObs and the
// query shape AWS will accept.
func TestBuildGetMetricDataQueries_VerifiesDimensionValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		key           composer.ComponentKey
		service       string
		resourceID    string
		wantCount     int
		wantNamespace string
		wantDimName   string
	}{
		{"ec2", composer.KeyAWSEC2, "ec2", "i-1234567890abcdef0", 5, "AWS/EC2", "InstanceId"},
		{"lambda", composer.KeyAWSLambda, "lambda", "my-function", 4, "AWS/Lambda", "FunctionName"},
		{"alb", composer.KeyAWSALB, "alb", "app/my-lb/1234567890", 3, "AWS/ApplicationELB", "LoadBalancer"},
		{"rds", composer.KeyAWSRDS, "rds", "my-db-instance", 3, "AWS/RDS", "DBInstanceIdentifier"},
		{"cloudfront", composer.KeyAWSCloudfront, "cloudfront", "E1234567890", 10, "AWS/CloudFront", "DistributionId"},
		{"apigateway", composer.KeyAWSAPIGateway, "apigateway", "abc123def4", 4, "AWS/ApiGateway", "ApiId"},
		{"vpc", composer.KeyAWSVPC, "vpc", "nat-0123456789abcdef0", 2, "AWS/NATGateway", "NatGatewayId"},
		{"s3", composer.KeyAWSS3, "s3", "my-bucket", 2, "AWS/S3", "BucketName"},
		{"sqs", composer.KeyAWSSQS, "sqs", "my-queue", 4, "AWS/SQS", "QueueName"},
		{"dynamodb", composer.KeyAWSDynamoDB, "dynamodb", "my-table", 4, "AWS/DynamoDB", "TableName"},
		{"cloudwatchlogs", composer.KeyAWSCloudWatchLogs, "cloudwatchlogs", "/aws/lambda/my-fn", 2, "AWS/Logs", "LogGroupName"},
		{"cognito", composer.KeyAWSCognito, "cognito", "us-east-1_ABC123", 3, "AWS/Cognito", "UserPoolId"},
		{"opensearch", composer.KeyAWSOpenSearch, "opensearch", "io-projx-search", 9, "AWS/ES", "DomainName"},
		{"bedrock", composer.KeyAWSBedrock, "bedrock", "anthropic.claude-3-5-sonnet", 6, "AWS/Bedrock", "ModelId"},
		// EKS panel queries ContainerInsights metrics keyed on
		// ClusterName (#233 Option B-1). The aws/eks_nodegroup
		// preset installs amazon-cloudwatch-observability by
		// default so the namespace is populated. Five metrics:
		// node_cpu/memory_utilization, pod_cpu/memory_utilization,
		// cluster_failed_node_count.
		{"eks", composer.KeyAWSEKS, "eks", "demo-cluster", 5, "ContainerInsights", "ClusterName"},
		{"elasticache", composer.KeyAWSElastiCache, "elasticache", "io-projx-redis-001", 8, "AWS/ElastiCache", "CacheClusterId"},
		{"msk", composer.KeyAWSMSK, "msk", "io-projx-kafka", 13, "AWS/Kafka", "Cluster Name"},
		{"waf", composer.KeyAWSWAF, "waf", "io-projx-webacl", 4, "AWS/WAFV2", "WebACL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			obs := awsSpecForService(t, tt.key, tt.service)
			res := ResourceID{ID: tt.resourceID, DimensionName: obs.DimensionName}
			queries := BuildGetMetricDataQueries(obs, res, tt.service)

			require.Len(t, queries, tt.wantCount)

			for _, q := range queries {
				require.NotNil(t, q.MetricStat)
				assert.Equal(t, tt.wantNamespace, aws.ToString(q.MetricStat.Metric.Namespace))

				dims := q.MetricStat.Metric.Dimensions
				require.NotEmpty(t, dims)
				assert.Equal(t, tt.wantDimName, aws.ToString(dims[0].Name))
				assert.Equal(t, tt.resourceID, aws.ToString(dims[0].Value), "resource ID must appear in dimension value")

				assert.NotEmpty(t, aws.ToString(q.MetricStat.Stat))
			}

			assert.Equal(t, obs.Metrics[0].Name, aws.ToString(queries[0].Label))
			assert.Equal(t, obs.Metrics[0].Stat, aws.ToString(queries[0].MetricStat.Stat))
		})
	}
}

// TestBuildGetMetricDataQueries_CloudFrontRegionGlobal mirrors reliable's
// aws_metrics_test.go:252. CloudFront metrics carry an extra
// Region=Global dimension so AWS knows we want the us-east-1-only
// publication and not some hypothetical regional split.
func TestBuildGetMetricDataQueries_CloudFrontRegionGlobal(t *testing.T) {
	t.Parallel()
	obs := awsSpecForService(t, composer.KeyAWSCloudfront, "cloudfront")
	res := ResourceID{ID: "E123456", DimensionName: "DistributionId"}
	queries := BuildGetMetricDataQueries(obs, res, "cloudfront")

	for _, q := range queries {
		dims := q.MetricStat.Metric.Dimensions
		require.Len(t, dims, 2, "CloudFront should have 2 dimensions: DistributionId + Region")
		assert.Equal(t, "DistributionId", aws.ToString(dims[0].Name))
		assert.Equal(t, "E123456", aws.ToString(dims[0].Value))
		assert.Equal(t, "Region", aws.ToString(dims[1].Name))
		assert.Equal(t, "Global", aws.ToString(dims[1].Value))
	}
}

// TestBuildGetMetricDataQueries_APIGatewayHTTPv2MetricNames pins the
// HTTP-API-v2 metric names ("4xx", "5xx", "Latency", "Count") into the
// query layer. Mirrors reliable's aws_metrics_test.go:275 — the
// regression that prompted the assertion was a flip back to v1 names
// ("4XXError" / "5XXError") which produced empty Pending-data panels.
func TestBuildGetMetricDataQueries_APIGatewayHTTPv2MetricNames(t *testing.T) {
	t.Parallel()
	obs := awsSpecForService(t, composer.KeyAWSAPIGateway, "apigateway")
	res := ResourceID{ID: "abc123def4", DimensionName: "ApiId"}
	queries := BuildGetMetricDataQueries(obs, res, "apigateway")

	got := make(map[string]struct{}, len(queries))
	for _, q := range queries {
		require.NotNil(t, q.MetricStat)
		got[aws.ToString(q.MetricStat.Metric.MetricName)] = struct{}{}
	}

	want := map[string]struct{}{
		"4xx":     {},
		"5xx":     {},
		"Latency": {},
		"Count":   {},
	}
	assert.Equal(t, want, got, "HTTP API v2 metric names must match what AWS publishes")
}

// TestBuildGetMetricDataQueries_CloudFrontAdditionalMetrics locks the
// full set of CloudFront metric names through the BuildGetMetricDataQueries
// path, including the additional-metrics surface unlocked by
// aws_cloudfront_monitoring_subscription. Mirrors reliable's
// aws_metrics_test.go:301.
func TestBuildGetMetricDataQueries_CloudFrontAdditionalMetrics(t *testing.T) {
	t.Parallel()
	obs := awsSpecForService(t, composer.KeyAWSCloudfront, "cloudfront")
	res := ResourceID{ID: "E1234567890", DimensionName: "DistributionId"}
	queries := BuildGetMetricDataQueries(obs, res, "cloudfront")

	got := make(map[string]struct{}, len(queries))
	for _, q := range queries {
		require.NotNil(t, q.MetricStat)
		got[aws.ToString(q.MetricStat.Metric.MetricName)] = struct{}{}
	}

	want := map[string]struct{}{
		"Requests":       {},
		"TotalErrorRate": {},
		"CacheHitRate":   {},
		"OriginLatency":  {},
		"401ErrorRate":   {},
		"403ErrorRate":   {},
		"404ErrorRate":   {},
		"502ErrorRate":   {},
		"503ErrorRate":   {},
		"504ErrorRate":   {},
	}
	assert.Equal(t, want, got)
}

// TestBuildGetMetricDataQueries_S3StorageType mirrors reliable's
// aws_metrics_test.go:327. S3 BucketSizeBytes uses StandardStorage,
// NumberOfObjects uses AllStorageTypes — get either wrong and AWS
// returns no datapoints.
func TestBuildGetMetricDataQueries_S3StorageType(t *testing.T) {
	t.Parallel()
	obs := awsSpecForService(t, composer.KeyAWSS3, "s3")
	res := ResourceID{ID: "my-bucket", DimensionName: "BucketName"}
	queries := BuildGetMetricDataQueries(obs, res, "s3")

	for _, q := range queries {
		dims := q.MetricStat.Metric.Dimensions
		require.Len(t, dims, 2, "S3 should have 2 dimensions: BucketName + StorageType")
		assert.Equal(t, "BucketName", aws.ToString(dims[0].Name))
		assert.Equal(t, "my-bucket", aws.ToString(dims[0].Value))
		assert.Equal(t, "StorageType", aws.ToString(dims[1].Name))

		label := aws.ToString(q.Label)
		storageType := aws.ToString(dims[1].Value)
		switch label {
		case "BucketSizeBytes":
			assert.Equal(t, "StandardStorage", storageType)
		case "NumberOfObjects":
			assert.Equal(t, "AllStorageTypes", storageType)
		}
	}
}

// TestBuildGetMetricDataQueries_NilObs returns nil rather than
// panicking. The Fetch caller already nil-guards but this matters for
// future direct callers (e.g. dashboard preview tooling).
func TestBuildGetMetricDataQueries_NilObs(t *testing.T) {
	t.Parallel()
	got := BuildGetMetricDataQueries(nil, ResourceID{ID: "i-abc"}, "ec2")
	assert.Nil(t, got)
}

// TestBuildGetMetricDataQueries_DimensionFallback exercises the
// per-resource DimensionName=="" fallback to obs.DimensionName.
// Callers that construct ResourceID from AWSObs.DimensionName once and
// reuse it for many resources can leave the per-record value blank.
func TestBuildGetMetricDataQueries_DimensionFallback(t *testing.T) {
	t.Parallel()
	obs := awsSpec(t, composer.KeyAWSEC2)
	queries := BuildGetMetricDataQueries(obs, ResourceID{ID: "i-blank"}, "ec2")
	require.NotEmpty(t, queries)
	dims := queries[0].MetricStat.Metric.Dimensions
	require.NotEmpty(t, dims)
	assert.Equal(t, "InstanceId", aws.ToString(dims[0].Name))
}

// --- getMetricData (the unexported CW response shaper) ---

func TestGetMetricData_HappyPath(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	ts1 := now.Add(-10 * time.Minute)
	ts2 := now.Add(-5 * time.Minute)

	mock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{
					Label:      aws.String("CPUUtilization"),
					Timestamps: []time.Time{ts1, ts2},
					Values:     []float64{45.5, 67.2},
				},
				{
					Label:      aws.String("NetworkIn"),
					Timestamps: []time.Time{ts1, ts2},
					Values:     []float64{1024, 2048},
				},
			},
		},
	}

	obs := awsSpec(t, composer.KeyAWSEC2)
	queries := BuildGetMetricDataQueries(obs, ResourceID{ID: "i-abc123"}, "ec2")
	results, err := getMetricData(context.Background(), mock, queries, now.Add(-time.Hour), now, 300)

	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "CPUUtilization", results[0].Name)
	require.Len(t, results[0].Datapoints, 2)
	assert.Equal(t, ts1.Format(time.RFC3339), results[0].Datapoints[0].Timestamp)
	assert.InDelta(t, 45.5, results[0].Datapoints[0].Average, 0.001)
	assert.InDelta(t, 67.2, results[0].Datapoints[1].Average, 0.001)

	assert.Equal(t, "NetworkIn", results[1].Name)
	assert.InDelta(t, 1024.0, results[1].Datapoints[0].Average, 0.001)

	require.NotNil(t, mock.lastInput)
	for _, q := range mock.lastInput.MetricDataQueries {
		if q.MetricStat != nil {
			assert.Equal(t, int32(300), aws.ToInt32(q.MetricStat.Period), "period should be overridden")
		}
	}
}

// TestGetMetricData_MismatchedTimestampsAndValues — a CloudWatch quirk
// where the response carries more timestamps than values. Truncate to
// the shorter of the two rather than panicking on out-of-bounds. Mirrors
// reliable's aws_metrics_test.go:400.
func TestGetMetricData_MismatchedTimestampsAndValues(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	ts1 := now.Add(-10 * time.Minute)
	ts2 := now.Add(-5 * time.Minute)
	ts3 := now

	mock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{
					Label:      aws.String("CPUUtilization"),
					Timestamps: []time.Time{ts1, ts2, ts3}, // 3 timestamps
					Values:     []float64{10.0, 20.0},      // only 2 values
				},
			},
		},
	}

	obs := awsSpec(t, composer.KeyAWSEC2)
	queries := BuildGetMetricDataQueries(obs, ResourceID{ID: "i-abc"}, "ec2")
	results, err := getMetricData(context.Background(), mock, queries, now.Add(-time.Hour), now, 300)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Len(t, results[0].Datapoints, 2)
}

func TestGetMetricData_EmptyResults(t *testing.T) {
	t.Parallel()
	mock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{},
		},
	}

	now := time.Now().UTC()
	obs := awsSpec(t, composer.KeyAWSEC2)
	queries := BuildGetMetricDataQueries(obs, ResourceID{ID: "i-abc"}, "ec2")
	results, err := getMetricData(context.Background(), mock, queries, now.Add(-time.Hour), now, 300)

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestGetMetricData_APIError(t *testing.T) {
	t.Parallel()
	mock := &fakeCloudWatch{err: errors.New("access denied")}

	now := time.Now().UTC()
	obs := awsSpec(t, composer.KeyAWSEC2)
	queries := BuildGetMetricDataQueries(obs, ResourceID{ID: "i-abc"}, "ec2")
	_, err := getMetricData(context.Background(), mock, queries, now.Add(-time.Hour), now, 300)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

// --- Fetch (orchestration) ---

func TestFetch_NilClients(t *testing.T) {
	t.Parallel()
	obs := awsSpec(t, composer.KeyAWSEC2)
	_, err := Fetch(context.Background(), nil, "ec2", obs, []ResourceID{{ID: "i-abc"}}, MetricsFilter{Hours: 6, Period: 300})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clients is required")
}

func TestFetch_NilObs(t *testing.T) {
	t.Parallel()
	c := clientsWithCW(&fakeCloudWatch{})
	_, err := Fetch(context.Background(), c, "ec2", nil, []ResourceID{{ID: "i-abc"}}, MetricsFilter{Hours: 6, Period: 300})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "obs is required")
}

func TestFetch_ZeroResources(t *testing.T) {
	t.Parallel()
	mock := &fakeCloudWatch{}
	c := clientsWithCW(mock)
	obs := awsSpec(t, composer.KeyAWSEC2)
	result, err := Fetch(context.Background(), c, "ec2", obs, nil, MetricsFilter{Hours: 6, Period: 300})

	require.NoError(t, err)
	assert.Equal(t, "ec2", result.Service)
	assert.Equal(t, "last 6 hours", result.TimeRange)
	assert.Equal(t, 300, result.Period)
	assert.NotNil(t, result.Resources, "Resources must be non-nil empty slice for JSON wire shape")
	assert.Empty(t, result.Resources)
	assert.Equal(t, 0, mock.calls, "no resources → no GetMetricData calls")
}

// TestFetch_S3PeriodAndHoursOverride mirrors reliable's
// aws_metrics_test.go:496 — S3 daily metrics force Period=86400 and
// bump Hours to >=48.
func TestFetch_S3PeriodAndHoursOverride(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{Label: aws.String("BucketSizeBytes"), Timestamps: []time.Time{now}, Values: []float64{1024}},
				{Label: aws.String("NumberOfObjects"), Timestamps: []time.Time{now}, Values: []float64{42}},
			},
		},
	}

	c := clientsWithCW(mock)
	obs := awsSpecForService(t, composer.KeyAWSS3, "s3")
	result, err := Fetch(context.Background(), c, "s3", obs,
		[]ResourceID{{ID: "my-bucket", DimensionName: "BucketName"}},
		MetricsFilter{Hours: 6, Period: 300})

	require.NoError(t, err)
	assert.Equal(t, 86400, result.Period, "S3 period must be 86400")
	assert.Contains(t, result.TimeRange, "48", "S3 hours must be bumped to at least 48")

	require.Len(t, result.Resources, 1)
	assert.Equal(t, "my-bucket", result.Resources[0].ResourceID)

	// Period override propagated to every query (via getMetricData).
	require.NotNil(t, mock.lastInput)
	for _, q := range mock.lastInput.MetricDataQueries {
		require.NotNil(t, q.MetricStat)
		assert.Equal(t, int32(86400), aws.ToInt32(q.MetricStat.Period))
	}
}

// TestFetch_S3DoesNotShortenLongerHours — caller-supplied Hours=72
// must NOT be clamped down to 48; the >=48 guard is a floor only. We
// also assert the actual GetMetricDataInput.StartTime/EndTime span
// reflects 72h, since the human-readable TimeRange string could in
// principle be derived independently of the API call.
func TestFetch_S3DoesNotShortenLongerHours(t *testing.T) {
	t.Parallel()
	mock := &fakeCloudWatch{output: &cloudwatch.GetMetricDataOutput{}}
	c := clientsWithCW(mock)
	obs := awsSpecForService(t, composer.KeyAWSS3, "s3")
	result, err := Fetch(context.Background(), c, "s3", obs,
		[]ResourceID{{ID: "my-bucket", DimensionName: "BucketName"}},
		MetricsFilter{Hours: 72, Period: 300})

	require.NoError(t, err)
	assert.Contains(t, result.TimeRange, "72")
	assert.Equal(t, 86400, result.Period)

	// Verify the API call interval reflects 72h, not the 48h floor.
	require.NotNil(t, mock.lastInput, "Fetch must invoke GetMetricData")
	require.NotNil(t, mock.lastInput.StartTime)
	require.NotNil(t, mock.lastInput.EndTime)
	span := mock.lastInput.EndTime.Sub(*mock.lastInput.StartTime)
	assert.Equal(t, 72*time.Hour, span,
		"GetMetricData interval must reflect the caller-supplied 72h, not the 48h floor")
}

// TestFetch_CloudFrontRoutesToUSEast1AndReturnsAllMetrics combines two
// production-shape contracts in one exercise. Mirrors reliable's
// aws_metrics_test.go:533.
//
//  1. CloudFront queries must go to the us-east-1 client, not the
//     caller's region client.
//  2. Every metric in the CloudFront spec must round-trip through
//     Fetch with its name → value mapping preserved (catches a regression
//     where a mislabeled MetricDataResult gets dropped or cross-wired).
func TestFetch_CloudFrontRoutesToUSEast1AndReturnsAllMetrics(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	defaultMock := &fakeCloudWatch{output: &cloudwatch.GetMetricDataOutput{}}

	cfObs := awsSpecForService(t, composer.KeyAWSCloudfront, "cloudfront")
	cfResults := make([]cwtypes.MetricDataResult, 0, len(cfObs.Metrics))
	wantValues := make(map[string]float64, len(cfObs.Metrics))
	for i, m := range cfObs.Metrics {
		v := float64(i + 1) // unique per metric so we catch cross-wiring
		wantValues[m.Name] = v
		cfResults = append(cfResults, cwtypes.MetricDataResult{
			Label:      aws.String(m.Name),
			Timestamps: []time.Time{now},
			Values:     []float64{v},
		})
	}
	cfMock := &fakeCloudWatch{output: &cloudwatch.GetMetricDataOutput{MetricDataResults: cfResults}}

	c := clientsWithCFOverride(defaultMock, cfMock)
	result, err := Fetch(context.Background(), c, "cloudfront", cfObs,
		[]ResourceID{{ID: "E123", DimensionName: "DistributionId"}},
		MetricsFilter{Hours: 6, Period: 300})

	require.NoError(t, err)
	require.Len(t, result.Resources, 1)

	// Contract 1: us-east-1 client was used.
	assert.Equal(t, 1, cfMock.calls, "CloudFront should use the us-east-1 client")
	assert.Equal(t, 0, defaultMock.calls, "Default client should NOT be called for CloudFront")

	// Contract 2: every metric round-trips by name → value.
	gotValues := make(map[string]float64, len(result.Resources[0].Metrics))
	for _, m := range result.Resources[0].Metrics {
		require.Len(t, m.Datapoints, 1, "metric %q should have one datapoint", m.Name)
		gotValues[m.Name] = m.Datapoints[0].Average
	}
	assert.Equal(t, wantValues, gotValues, "every cloudfront metric must round-trip by name → value")
}

// TestFetch_PartialResourceFailure_Skips mirrors reliable's
// aws_metrics_test.go:585 — when GetMetricData fails for every
// resource, Fetch returns a no-error empty-resources result rather
// than aborting (so chart panels render "no data" not "broken").
func TestFetch_PartialResourceFailure_Skips(t *testing.T) {
	t.Parallel()
	mock := &fakeCloudWatch{err: errors.New("throttled")}
	c := clientsWithCW(mock)
	obs := awsSpec(t, composer.KeyAWSEC2)
	result, err := Fetch(context.Background(), c, "ec2", obs,
		[]ResourceID{{ID: "i-good"}, {ID: "i-bad"}},
		MetricsFilter{Hours: 6, Period: 300})

	require.NoError(t, err, "partial failures should not bubble up as errors")
	assert.Empty(t, result.Resources, "failed resources should be skipped")
	assert.Equal(t, "ec2", result.Service)
	assert.Equal(t, 2, mock.calls, "every resource is attempted even after a failure")
}

// TestFetch_EC2_EndToEnd is the canonical happy-path end-to-end exercise.
// Mirrors reliable's aws_metrics_test.go:605.
func TestFetch_EC2_EndToEnd(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	mock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{
			MetricDataResults: []cwtypes.MetricDataResult{
				{Label: aws.String("CPUUtilization"), Timestamps: []time.Time{now}, Values: []float64{35.5}},
				{Label: aws.String("NetworkIn"), Timestamps: []time.Time{now}, Values: []float64{8192}},
				{Label: aws.String("NetworkOut"), Timestamps: []time.Time{now}, Values: []float64{4096}},
				{Label: aws.String("DiskReadOps"), Timestamps: []time.Time{now}, Values: []float64{100}},
				{Label: aws.String("DiskWriteOps"), Timestamps: []time.Time{now}, Values: []float64{200}},
			},
		},
	}

	c := clientsWithCW(mock)
	obs := awsSpec(t, composer.KeyAWSEC2)
	result, err := Fetch(context.Background(), c, "ec2", obs,
		[]ResourceID{{ID: "i-abc123", DimensionName: "InstanceId"}},
		MetricsFilter{Hours: 12, Period: 600})

	require.NoError(t, err)
	assertValidMetricsResult(t, result, "ec2", obs)

	assert.Equal(t, 600, result.Period)
	assert.Contains(t, result.TimeRange, "12")
	require.Len(t, result.Resources, 1)
	assert.Equal(t, "i-abc123", result.Resources[0].ResourceID)
	require.Len(t, result.Resources[0].Metrics, 5)
	assert.Equal(t, "CPUUtilization", result.Resources[0].Metrics[0].Name)
}

// TestFetch_ClampsHugePeriodToOneDay locks the period clamp at 86400
// (the max GetMetricData accepts). Anyone passing Period=999999 (an
// older default in some inspector code paths) gets the supported
// ceiling instead of an AWS-side validation error.
func TestFetch_ClampsHugePeriodToOneDay(t *testing.T) {
	t.Parallel()
	mock := &fakeCloudWatch{
		output: &cloudwatch.GetMetricDataOutput{},
	}
	c := clientsWithCW(mock)
	obs := awsSpec(t, composer.KeyAWSEC2)
	_, err := Fetch(context.Background(), c, "ec2", obs,
		[]ResourceID{{ID: "i-abc", DimensionName: "InstanceId"}},
		MetricsFilter{Hours: 6, Period: 9_999_999})

	require.NoError(t, err)
	require.NotNil(t, mock.lastInput)
	for _, q := range mock.lastInput.MetricDataQueries {
		require.NotNil(t, q.MetricStat)
		assert.Equal(t, int32(86400), aws.ToInt32(q.MetricStat.Period))
	}
}

// --- Spec-coverage drift tests ---

// TestEveryAlarmedAWSSpecHasMetricFetchCoverage walks every component
// key whose AWS spec has at least one metric and confirms the spec
// passes through BuildGetMetricDataQueries to a non-empty query slice
// with the expected dimension. Catches drift between the per-component
// authority (observability.AWSObs) and the metric-fetch builder.
//
// Stops short of asserting Fetch end-to-end per key (that's covered
// for ec2/s3/cloudfront above) — the drift this test catches is "a key
// landed in the authority but its dimension name doesn't survive the
// builder".
func TestEveryAWSSpecBuildsValidQueries(t *testing.T) {
	t.Parallel()
	for _, k := range composer.AllComponentKeys {
		o, ok := observability.Lookup(k)
		if !ok || o.AWS == nil || len(o.AWS.Metrics) == 0 {
			continue
		}
		t.Run(string(k), func(t *testing.T) {
			t.Parallel()
			res := ResourceID{ID: "test-id", DimensionName: o.AWS.DimensionName}
			queries := BuildGetMetricDataQueries(o.AWS, res, o.Service)
			require.Len(t, queries, len(o.AWS.Metrics),
				"%s: query count must match spec metric count", k)
			for i, q := range queries {
				require.NotNil(t, q.MetricStat, "%s metric[%d]: MetricStat must be set", k, i)
				assert.Equal(t, o.AWS.Namespace, aws.ToString(q.MetricStat.Metric.Namespace),
					"%s metric[%d]: namespace drift", k, i)
				assert.Equal(t, o.AWS.Metrics[i].Name, aws.ToString(q.MetricStat.Metric.MetricName),
					"%s metric[%d]: name drift", k, i)
				assert.Equal(t, o.AWS.Metrics[i].Stat, aws.ToString(q.MetricStat.Stat),
					"%s metric[%d]: stat drift", k, i)
				dims := q.MetricStat.Metric.Dimensions
				require.NotEmpty(t, dims, "%s metric[%d]: must carry the resource dimension", k, i)
				assert.Equal(t, o.AWS.DimensionName, aws.ToString(dims[0].Name),
					"%s metric[%d]: dimension name drift", k, i)
			}
		})
	}
}

// TestAllSpecsHaveValidStats — every AWS spec stat must be one of the
// CloudWatch-recognised aggregates. Mirrors reliable's
// aws_metrics_test.go:942.
func TestAllSpecsHaveValidStats(t *testing.T) {
	t.Parallel()
	validStats := map[string]bool{
		"Average": true, "Sum": true, "Maximum": true, "Minimum": true,
	}
	for _, k := range composer.AllComponentKeys {
		o, ok := observability.Lookup(k)
		if !ok || o.AWS == nil {
			continue
		}
		for _, m := range o.AWS.Metrics {
			assert.True(t, validStats[m.Stat],
				"key %s metric %s has invalid stat: %s", k, m.Name, m.Stat)
		}
	}
}

// --- Shared structural validator ---

// assertValidMetricsResult validates the structural contract of a
// MetricsResult. Mirrors reliable's assertValidAWSMetricsResult
// (aws_metrics_test.go:89). Both unit tests and any future integration
// tests should call this so the mock output and any real-API output
// are validated by the same shape contract.
func assertValidMetricsResult(t *testing.T, result MetricsResult, service string, obs *observability.AWSObs) {
	t.Helper()

	assert.Equal(t, service, result.Service)
	assert.NotEmpty(t, result.TimeRange)
	assert.Greater(t, result.Period, 0)

	validNames := make(map[string]bool, len(obs.Metrics))
	for _, m := range obs.Metrics {
		validNames[m.Name] = true
	}

	for _, res := range result.Resources {
		assert.NotEmpty(t, res.ResourceID, "resource ID must not be empty")
		for _, m := range res.Metrics {
			assert.True(t, validNames[m.Name],
				"unexpected metric name %q for service %s (valid: %v)", m.Name, service, validNames)
			for _, dp := range m.Datapoints {
				assert.NotEmpty(t, dp.Timestamp, "datapoint timestamp must not be empty")
				_, parseErr := time.Parse(time.RFC3339, dp.Timestamp)
				assert.NoError(t, parseErr, "invalid RFC3339 timestamp: %s", dp.Timestamp)
			}
		}
	}
}
