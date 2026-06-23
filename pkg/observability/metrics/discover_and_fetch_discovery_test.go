package metrics

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	apigatewayv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	cwlogstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the per-service CloudWatch-dimension extractors that
// DiscoverAndFetch's auto-discovery path depends on. Reliable did not
// unit-test these (the extraction was implicitly covered by the
// discovery/aws Inspect tests + integration tests), but porting the
// behavior across repos warrants direct coverage of the
// special-case shape handling (running-only EC2, ALB ARN suffix, SQS
// URL tail, ECS ClusterName-vs-ARN, Bedrock no-metrics, and the
// typed-vs-map dual paths).

// TestExtractEC2RunningInstanceIDs_OnlyRunning verifies stopped /
// terminated instances are excluded — only running instances publish the
// metrics we chart.
func TestExtractEC2RunningInstanceIDs_OnlyRunning(t *testing.T) {
	t.Parallel()
	raw := []ec2types.Reservation{
		{Instances: []ec2types.Instance{
			{InstanceId: aws.String("i-running"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
			{InstanceId: aws.String("i-stopped"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameStopped}},
		}},
		{Instances: []ec2types.Instance{
			{InstanceId: aws.String("i-running2"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
			{InstanceId: aws.String("i-nostate")}, // nil State → skipped
		}},
	}
	assert.Equal(t, []string{"i-running", "i-running2"}, extractEC2RunningInstanceIDs(any(raw)))

	// Wrong underlying type → nil (defensive, matches reliable).
	assert.Nil(t, extractEC2RunningInstanceIDs(any([]string{"x"})))
}

func TestExtractLambdaFunctionNames(t *testing.T) {
	t.Parallel()
	raw := []lambdatypes.FunctionConfiguration{
		{FunctionName: aws.String("fn-a")},
		{FunctionName: aws.String("")}, // empty → skipped
		{FunctionName: aws.String("fn-b")},
	}
	assert.Equal(t, []string{"fn-a", "fn-b"}, extractLambdaFunctionNames(any(raw)))
}

// TestExtractALBDimensionIDs pins the ARN→suffix trim: AWS keys
// AWS/ApplicationELB on "app/<name>/<hash>", not the full ARN.
func TestExtractALBDimensionIDs(t *testing.T) {
	t.Parallel()
	raw := []elbv2types.LoadBalancer{
		{LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:eu-west-2:111:loadbalancer/app/my-lb/abc123")},
		{LoadBalancerArn: aws.String("not-an-arn")}, // no "loadbalancer/" → skipped
	}
	assert.Equal(t, []string{"app/my-lb/abc123"}, extractALBDimensionIDs(any(raw)))
}

// TestExtractRDSInstanceIdentifiers_BothShapes covers the typed-slice
// (project=="") path and the []map[string]any (project!="", filter.Match)
// path.
func TestExtractRDSInstanceIdentifiers_BothShapes(t *testing.T) {
	t.Parallel()
	typed := []rdstypes.DBInstance{{DBInstanceIdentifier: aws.String("db-1")}, {DBInstanceIdentifier: aws.String("db-2")}}
	assert.Equal(t, []string{"db-1", "db-2"}, extractRDSInstanceIdentifiers(any(typed)))

	maps := []map[string]any{{"DBInstanceIdentifier": "db-3"}, {"OtherField": "x"}, {"DBInstanceIdentifier": "db-4"}}
	assert.Equal(t, []string{"db-3", "db-4"}, extractRDSInstanceIdentifiers(any(maps)))
}

func TestExtractCloudFrontDistributionIDs_BothShapes(t *testing.T) {
	t.Parallel()
	typed := []cloudfronttypes.DistributionSummary{{Id: aws.String("E123")}}
	assert.Equal(t, []string{"E123"}, extractCloudFrontDistributionIDs(any(typed)))
	maps := []map[string]any{{"Id": "E456"}}
	assert.Equal(t, []string{"E456"}, extractCloudFrontDistributionIDs(any(maps)))
}

func TestExtractAPIGatewayIDs_BothShapes(t *testing.T) {
	t.Parallel()
	typed := []apigatewayv2types.Api{{ApiId: aws.String("api-1")}}
	assert.Equal(t, []string{"api-1"}, extractAPIGatewayIDs(any(typed)))
	maps := []map[string]any{{"ApiId": "api-2"}}
	assert.Equal(t, []string{"api-2"}, extractAPIGatewayIDs(any(maps)))
}

func TestExtractNATGatewayIDs(t *testing.T) {
	t.Parallel()
	raw := []ec2types.NatGateway{{NatGatewayId: aws.String("nat-1")}, {NatGatewayId: aws.String("nat-2")}}
	assert.Equal(t, []string{"nat-1", "nat-2"}, extractNATGatewayIDs(any(raw)))
}

func TestExtractS3BucketNames(t *testing.T) {
	t.Parallel()
	raw := []s3types.Bucket{{Name: aws.String("bucket-a")}, {Name: aws.String("bucket-b")}}
	assert.Equal(t, []string{"bucket-a", "bucket-b"}, extractS3BucketNames(any(raw)))
}

// TestExtractSQSQueueNames pins the URL→name tail extraction (AWS/SQS
// keys QueueName, not the full URL).
func TestExtractSQSQueueNames(t *testing.T) {
	t.Parallel()
	raw := []string{
		"https://sqs.eu-west-2.amazonaws.com/111/my-queue",
		"https://sqs.eu-west-2.amazonaws.com/111/",   // trailing slash → skipped
		"https://sqs.eu-west-2.amazonaws.com/111/q2", //
	}
	assert.Equal(t, []string{"my-queue", "q2"}, extractSQSQueueNames(any(raw)))
}

func TestExtractStringSlice(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"t1", "t2"}, extractStringSlice(any([]string{"t1", "t2"})))
	assert.Nil(t, extractStringSlice(any(123)))
}

func TestExtractCloudWatchLogGroupNames(t *testing.T) {
	t.Parallel()
	raw := []cwlogstypes.LogGroup{{LogGroupName: aws.String("/aws/lambda/fn")}}
	assert.Equal(t, []string{"/aws/lambda/fn"}, extractCloudWatchLogGroupNames(any(raw)))
}

func TestExtractCognitoUserPoolIDs(t *testing.T) {
	t.Parallel()
	raw := []cognitoidptypes.UserPoolDescriptionType{{Id: aws.String("eu-west-2_ABC")}}
	assert.Equal(t, []string{"eu-west-2_ABC"}, extractCognitoUserPoolIDs(any(raw)))
}

// TestExtractOpenSearchDomainNames covers the map path and the
// typed-via-JSON fallback path.
func TestExtractOpenSearchDomainNames(t *testing.T) {
	t.Parallel()
	maps := []map[string]any{{"DomainName": "search-1"}}
	assert.Equal(t, []string{"search-1"}, extractOpenSearchDomainNames(any(maps)))

	// A typed shape that JSON-marshals to objects carrying "DomainName".
	type domainStatus struct {
		DomainName string `json:"DomainName"`
	}
	typed := []domainStatus{{DomainName: "search-2"}}
	assert.Equal(t, []string{"search-2"}, extractOpenSearchDomainNames(any(typed)))
}

// TestExtractBedrockNoMetrics always returns nil — Bedrock metrics are
// per-foundation-model and not project-scoped (reliable#1017/#1018).
func TestExtractBedrockNoMetrics(t *testing.T) {
	t.Parallel()
	assert.Nil(t, extractBedrockNoMetrics(any([]string{"anything"})))
	assert.Nil(t, extractBedrockNoMetrics(nil))
}

func TestExtractElastiCacheClusterIDs(t *testing.T) {
	t.Parallel()
	raw := []elasticachetypes.CacheCluster{{CacheClusterId: aws.String("redis-001")}}
	assert.Equal(t, []string{"redis-001"}, extractElastiCacheClusterIDs(any(raw)))
}

func TestExtractMSKClusterARNs_BothShapes(t *testing.T) {
	t.Parallel()
	typed := []kafkatypes.Cluster{{ClusterArn: aws.String("arn:aws:kafka:...:cluster/io-kafka/x")}}
	assert.Equal(t, []string{"arn:aws:kafka:...:cluster/io-kafka/x"}, extractMSKClusterARNs(any(typed)))
	maps := []map[string]any{{"ClusterArn": "arn:map"}}
	assert.Equal(t, []string{"arn:map"}, extractMSKClusterARNs(any(maps)))
}

func TestExtractWAFWebACLIDs(t *testing.T) {
	t.Parallel()
	raw := []wafv2types.WebACLSummary{{Id: aws.String("acl-1")}}
	assert.Equal(t, []string{"acl-1"}, extractWAFWebACLIDs(any(raw)))
}

// TestExtractECSClusterNames pins the ClusterName-preferred,
// ARN-suffix-fallback behavior (AWS/ECS keys ClusterName, not ARN).
func TestExtractECSClusterNames(t *testing.T) {
	t.Parallel()
	raw := []ecstypes.Cluster{
		{ClusterName: aws.String("named-cluster")},
		{ClusterArn: aws.String("arn:aws:ecs:eu-west-2:111:cluster/arn-only-cluster")}, // ClusterName empty → ARN tail
	}
	assert.Equal(t, []string{"named-cluster", "arn-only-cluster"}, extractECSClusterNames(any(raw)))
}

func TestExtractStringField_NonMapReturnsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, extractStringField(any([]string{"x"}), "Field"))
	assert.Equal(t, []string{"v"}, extractStringField(any([]map[string]any{{"Field": "v"}, {"Field": 5}}), "Field"))
}

// TestMetricsDiscoveryActions_AllMetricServicesCovered is the reliable
// drift guard: every service in metricDefinitions must have a discovery
// action, except the allowlisted services whose upstream inspect handler
// exposes no list-enumeration action usable to resolve a CloudWatch
// dimension value. kms / secretsmanager are excluded by construction
// (operational-health path, not in metricDefinitions).
//
// IMPORTANT (parity note): the allowlisted services below have a metric
// CATALOG entry (observability.AWSObs) but NO account-wide discovery
// action. DiscoverAndFetch's auto-discovery path therefore errors with
// "no auto-discovery for service: <svc>" for them — exactly as reliable's
// getServiceMetrics does today (reliable's metricsDiscoveryActions never
// covered these). The resource-scoped fast path (#2035) still works since
// it bypasses discovery. This allowlist can only SHRINK: add a discovery
// row upstream and delete the entry here.
func TestMetricsDiscoveryActions_AllMetricServicesCovered(t *testing.T) {
	t.Parallel()
	exempt := map[string]bool{
		// presets#797: sagemaker inspect exposes no endpoint-listing
		// action, so account-wide discovery of the EndpointName dimension
		// is impossible. Binding-scoped fast path (#2035) still works.
		"sagemaker": true,
		// kendra / agentcore: upstream grew AWS metric specs for these
		// after reliable's metricsDiscoveryActions was written; their
		// inspect handlers expose no list action that yields the metric
		// dimension value, so account-wide discovery is unavailable — same
		// situation as sagemaker. Auto-discovery errors; fast path works.
		"kendra":    true,
		"agentcore": true,
	}
	for svc := range metricDefinitions {
		if exempt[svc] {
			continue
		}
		_, ok := metricsDiscoveryActions[svc]
		assert.True(t, ok, "service %s has metric definitions but no discovery action", svc)
	}
}

// TestMetricDefinitions_KMSAndSMExcluded confirms the catalog view skips
// kms / secretsmanager (they take the operational-health path), matching
// reliable.
func TestMetricDefinitions_KMSAndSMExcluded(t *testing.T) {
	t.Parallel()
	_, hasKMS := metricDefinitions["kms"]
	_, hasSM := metricDefinitions["secretsmanager"]
	assert.False(t, hasKMS, "kms must not be in metricDefinitions")
	assert.False(t, hasSM, "secretsmanager must not be in metricDefinitions")

	// And the canonical EC2 service must be present with its AWSObs spec
	// sourced from the upstream observability registry. (Pointer identity
	// against a specific component key is intentionally NOT asserted —
	// several component keys map to Service=="ec2" and map iteration order
	// picks the first-seen, matching reliable's metricDefinitions build.)
	ec2Obs, ok := metricDefinitions["ec2"]
	require.True(t, ok, "ec2 must be in metricDefinitions")
	require.NotNil(t, ec2Obs)
	assert.Equal(t, "AWS/EC2", ec2Obs.Namespace)
	assert.Equal(t, "InstanceId", ec2Obs.DimensionName)
}
