// Compute-tier inspector tests. Covers ECS cluster + service filtering,
// EKS cluster filtering, Lambda function filtering, and the EC2
// instance-connect URL enrichment.
//
// We never call real AWS — every test injects a fake client implementing
// the per-service interface (ecsClient, eksClustersClient,
// lambdaFunctionsClient). Mirrors the reliable testing pattern.

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ECS fake ---

type fakeECSClient struct {
	listClustersOut     *ecs.ListClustersOutput
	listClustersErr     error
	describeClustersOut *ecs.DescribeClustersOutput
	describeClustersErr error
	listServicesOut     *ecs.ListServicesOutput
	listServicesErr     error
	describeServicesOut *ecs.DescribeServicesOutput
	describeServicesErr error
}

func (f *fakeECSClient) ListClusters(_ context.Context, _ *ecs.ListClustersInput, _ ...func(*ecs.Options)) (*ecs.ListClustersOutput, error) {
	if f.listClustersErr != nil {
		return nil, f.listClustersErr
	}
	if f.listClustersOut == nil {
		return &ecs.ListClustersOutput{}, nil
	}
	return f.listClustersOut, nil
}

func (f *fakeECSClient) DescribeClusters(_ context.Context, _ *ecs.DescribeClustersInput, _ ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error) {
	if f.describeClustersErr != nil {
		return nil, f.describeClustersErr
	}
	if f.describeClustersOut == nil {
		return &ecs.DescribeClustersOutput{}, nil
	}
	return f.describeClustersOut, nil
}

func (f *fakeECSClient) ListServices(_ context.Context, _ *ecs.ListServicesInput, _ ...func(*ecs.Options)) (*ecs.ListServicesOutput, error) {
	if f.listServicesErr != nil {
		return nil, f.listServicesErr
	}
	if f.listServicesOut == nil {
		return &ecs.ListServicesOutput{}, nil
	}
	return f.listServicesOut, nil
}

func (f *fakeECSClient) DescribeServices(_ context.Context, _ *ecs.DescribeServicesInput, _ ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	if f.describeServicesErr != nil {
		return nil, f.describeServicesErr
	}
	if f.describeServicesOut == nil {
		return &ecs.DescribeServicesOutput{}, nil
	}
	return f.describeServicesOut, nil
}

func TestFilterECSClustersByProjectTag_Match(t *testing.T) {
	t.Parallel()
	client := &fakeECSClient{
		listClustersOut: &ecs.ListClustersOutput{
			ClusterArns: []string{"arn:aws:ecs:us-east-1:111:cluster/foo", "arn:aws:ecs:us-east-1:111:cluster/bar"},
		},
		describeClustersOut: &ecs.DescribeClustersOutput{
			Clusters: []ecstypes.Cluster{
				{
					ClusterArn:  aws.String("arn:aws:ecs:us-east-1:111:cluster/foo"),
					ClusterName: aws.String("foo"),
					Tags:        []ecstypes.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
				},
				{
					ClusterArn:  aws.String("arn:aws:ecs:us-east-1:111:cluster/bar"),
					ClusterName: aws.String("bar"),
					Tags:        []ecstypes.Tag{{Key: aws.String("Project"), Value: aws.String("other")}},
				},
			},
		},
	}
	got, err := filterECSClustersByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "foo", aws.ToString(got[0].ClusterName))
}

func TestFilterECSClustersByProjectTag_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	client := &fakeECSClient{
		listClustersOut: &ecs.ListClustersOutput{
			ClusterArns: []string{"arn:aws:ecs:us-east-1:111:cluster/foo"},
		},
		describeClustersOut: &ecs.DescribeClustersOutput{
			Clusters: []ecstypes.Cluster{
				{ClusterArn: aws.String("arn:aws:ecs:us-east-1:111:cluster/foo")},
			},
		},
	}
	got, err := filterECSClustersByProjectTag(context.Background(), client, "")
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestFilterECSClustersByProjectTag_NoArnsShortCircuits(t *testing.T) {
	t.Parallel()
	client := &fakeECSClient{
		listClustersOut: &ecs.ListClustersOutput{},
	}
	got, err := filterECSClustersByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFilterECSClustersByProjectTag_ListClustersError(t *testing.T) {
	t.Parallel()
	client := &fakeECSClient{listClustersErr: errors.New("boom")}
	_, err := filterECSClustersByProjectTag(context.Background(), client, "my-stack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ecs ListClusters")
}

func TestHasProjectTagECS(t *testing.T) {
	t.Parallel()
	tags := []ecstypes.Tag{
		{Key: aws.String("Owner"), Value: aws.String("alice")},
		{Key: aws.String("Project"), Value: aws.String("my-stack")},
	}
	assert.True(t, hasProjectTagECS(tags, "my-stack"))
	assert.False(t, hasProjectTagECS(tags, "other"))
	// Empty project means "no scope" → matches everything (the demo
	// session fallback reliable preserves).
	assert.True(t, hasProjectTagECS(tags, ""))
}

// --- EKS fake ---

type fakeEKSClient struct {
	listClustersOut    *eks.ListClustersOutput
	listClustersErr    error
	describeClusterOut *eks.DescribeClusterOutput
	describeClusterErr error
}

func (f *fakeEKSClient) ListClusters(_ context.Context, _ *eks.ListClustersInput, _ ...func(*eks.Options)) (*eks.ListClustersOutput, error) {
	if f.listClustersErr != nil {
		return nil, f.listClustersErr
	}
	if f.listClustersOut == nil {
		return &eks.ListClustersOutput{}, nil
	}
	return f.listClustersOut, nil
}

func (f *fakeEKSClient) DescribeCluster(_ context.Context, _ *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	if f.describeClusterErr != nil {
		return nil, f.describeClusterErr
	}
	if f.describeClusterOut == nil {
		return &eks.DescribeClusterOutput{}, nil
	}
	return f.describeClusterOut, nil
}

func TestFilterEKSClustersByProjectTag_Match(t *testing.T) {
	t.Parallel()
	client := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{
			Clusters: []string{"foo", "bar"},
		},
		// DescribeCluster is called per-name; we return the same canned
		// output for both. The Tags map decides the filter outcome —
		// here, only the Project=my-stack tag matches so the result of
		// the second DescribeCluster (also tagged my-stack via the same
		// fake) means BOTH end up matched. To get a single match we'd
		// need a per-name response — beyond a happy-path test scope.
		describeClusterOut: &eks.DescribeClusterOutput{
			Cluster: &ekstypes.Cluster{
				Name: aws.String("matched"),
				Tags: map[string]string{"Project": "my-stack"},
			},
		},
	}
	got, err := filterEKSClustersByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestFilterEKSClustersByProjectTag_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	client := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{
			Clusters: []string{"foo", "bar", "baz"},
		},
	}
	got, err := filterEKSClustersByProjectTag(context.Background(), client, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"foo", "bar", "baz"}, got)
}

func TestFilterEKSClustersByProjectTag_DescribeErrorSkips(t *testing.T) {
	t.Parallel()
	// DescribeCluster errors are log-skipped — fail-closed. With the
	// project filter active and the only cluster's describe failing,
	// the result is empty (zero matches), not an error.
	client := &fakeEKSClient{
		listClustersOut:    &eks.ListClustersOutput{Clusters: []string{"foo"}},
		describeClusterErr: errors.New("denied"),
	}
	got, err := filterEKSClustersByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// --- Lambda fake ---

type fakeLambdaClient struct {
	listFunctionsOut *lambda.ListFunctionsOutput
	listFunctionsErr error
	listTagsOut      *lambda.ListTagsOutput
	listTagsErr      error
}

func (f *fakeLambdaClient) ListFunctions(_ context.Context, _ *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	if f.listFunctionsErr != nil {
		return nil, f.listFunctionsErr
	}
	if f.listFunctionsOut == nil {
		return &lambda.ListFunctionsOutput{}, nil
	}
	return f.listFunctionsOut, nil
}

func (f *fakeLambdaClient) ListTags(_ context.Context, _ *lambda.ListTagsInput, _ ...func(*lambda.Options)) (*lambda.ListTagsOutput, error) {
	if f.listTagsErr != nil {
		return nil, f.listTagsErr
	}
	if f.listTagsOut == nil {
		return &lambda.ListTagsOutput{}, nil
	}
	return f.listTagsOut, nil
}

func TestFilterLambdaFunctionsByProjectTag_Match(t *testing.T) {
	t.Parallel()
	client := &fakeLambdaClient{
		listFunctionsOut: &lambda.ListFunctionsOutput{
			Functions: []lambdatypes.FunctionConfiguration{
				{FunctionArn: aws.String("arn:aws:lambda:us-east-1:1:function:f1"), FunctionName: aws.String("f1")},
				{FunctionArn: aws.String("arn:aws:lambda:us-east-1:1:function:f2"), FunctionName: aws.String("f2")},
			},
		},
		listTagsOut: &lambda.ListTagsOutput{
			Tags: map[string]string{"Project": "my-stack"},
		},
	}
	got, err := filterLambdaFunctionsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestFilterLambdaFunctionsByProjectTag_EmptyProjectShortCircuits(t *testing.T) {
	t.Parallel()
	client := &fakeLambdaClient{
		listFunctionsOut: &lambda.ListFunctionsOutput{
			Functions: []lambdatypes.FunctionConfiguration{
				{FunctionArn: aws.String("arn:aws:lambda:us-east-1:1:function:f1")},
			},
		},
		// listTagsOut intentionally nil — empty project must NOT
		// trigger the per-function tag fan-out.
	}
	got, err := filterLambdaFunctionsByProjectTag(context.Background(), client, "")
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestFilterLambdaFunctionsByProjectTag_TagsErrorSkips(t *testing.T) {
	t.Parallel()
	client := &fakeLambdaClient{
		listFunctionsOut: &lambda.ListFunctionsOutput{
			Functions: []lambdatypes.FunctionConfiguration{
				{FunctionArn: aws.String("arn:aws:lambda:us-east-1:1:function:f1")},
			},
		},
		listTagsErr: errors.New("denied"),
	}
	got, err := filterLambdaFunctionsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err) // log+skip, not abort
	assert.Empty(t, got)
}

// --- EC2 instance-connect URL enrichment ---

func TestEnrichEC2WithConnectURLs_RunningInstanceGetsURL(t *testing.T) {
	t.Parallel()
	reservations := []ec2types.Reservation{
		{
			Instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-running"),
					State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
				},
				{
					InstanceId: aws.String("i-stopped"),
					State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameStopped},
				},
			},
		},
	}
	out := enrichEC2WithConnectURLs("us-east-1", reservations)
	enriched, ok := out.([]map[string]any)
	require.True(t, ok, "expected []map[string]any after enrichment")
	require.Len(t, enriched, 1)
	instances, ok := enriched[0]["Instances"].([]any)
	require.True(t, ok)
	require.Len(t, instances, 2)

	running := instances[0].(map[string]any)
	stopped := instances[1].(map[string]any)
	assert.Contains(t, running["InstanceConnectURL"], "i-running")
	assert.Contains(t, running["InstanceConnectURL"], "us-east-1")
	_, hasURL := stopped["InstanceConnectURL"]
	assert.False(t, hasURL, "stopped instances must NOT receive an InstanceConnectURL")
}

func TestEnrichEC2WithConnectURLs_EmptyReservationsReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	out := enrichEC2WithConnectURLs("us-east-1", nil)
	// Either the empty []map[string]any or the original nil slice is
	// acceptable per the helper's fall-through contract.
	switch v := out.(type) {
	case []map[string]any:
		assert.Empty(t, v)
	case []ec2types.Reservation:
		assert.Empty(t, v)
	default:
		t.Fatalf("unexpected return type: %T", out)
	}
}
