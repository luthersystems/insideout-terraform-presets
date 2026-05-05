// Compute-tier inspector tests. Covers ECS cluster + service filtering,
// EKS cluster filtering, Lambda function filtering, and the EC2
// instance-connect URL enrichment.
//
// We never call real AWS — every test injects a fake client implementing
// the per-service interface (ecsClient, eksClustersClient,
// lambdaFunctionsClient). Mirrors the InsideOut backend testing pattern.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
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
	// session fallback the InsideOut backend preserves).
	assert.True(t, hasProjectTagECS(tags, ""))
}

// --- EKS fake ---

// fakeEKSClient routes DescribeCluster per-cluster-name via the
// describeByName map so a test can return distinct responses per cluster
// (e.g. one tagged Project=my-stack, one tagged Project=other) and verify
// the filter actually selects on the tag value. The single-output
// describeClusterOut field is retained for tests that don't need
// per-name routing.
type fakeEKSClient struct {
	listClustersOut    *eks.ListClustersOutput
	listClustersErr    error
	describeClusterOut *eks.DescribeClusterOutput
	describeClusterErr error
	describeByName     map[string]*eks.DescribeClusterOutput
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

func (f *fakeEKSClient) DescribeCluster(_ context.Context, in *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	if f.describeClusterErr != nil {
		return nil, f.describeClusterErr
	}
	if f.describeByName != nil {
		if out, ok := f.describeByName[aws.ToString(in.Name)]; ok {
			return out, nil
		}
		return &eks.DescribeClusterOutput{}, nil
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
		// Per-name routing: foo carries the matching Project tag, bar
		// does not. If the filter is a no-op the test fails because both
		// clusters would come back.
		describeByName: map[string]*eks.DescribeClusterOutput{
			"foo": {Cluster: &ekstypes.Cluster{
				Name: aws.String("foo"),
				Tags: map[string]string{"Project": "my-stack"},
			}},
			"bar": {Cluster: &ekstypes.Cluster{
				Name: aws.String("bar"),
				Tags: map[string]string{"Project": "other"},
			}},
		},
	}
	got, err := filterEKSClustersByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "foo", got[0])
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

// --- EC2 fake (for EKS list-nodes fan-out) ---

// fakeEC2InstancesClient records the filters it observes so tests can
// assert per-cluster filter shape, and returns per-cluster
// DescribeInstances results keyed off the `tag:eks:cluster-name`
// filter value. errByCluster lets a test simulate IAM denials /
// throttles on a subset of clusters to exercise the log+skip path.
type fakeEC2InstancesClient struct {
	reservationsByCluster map[string][]ec2types.Reservation
	errByCluster          map[string]error
	calls                 []ec2.DescribeInstancesInput
}

func (f *fakeEC2InstancesClient) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if in != nil {
		f.calls = append(f.calls, *in)
	}
	cluster := ""
	for _, ft := range in.Filters {
		if aws.ToString(ft.Name) == "tag:eks:cluster-name" && len(ft.Values) > 0 {
			cluster = ft.Values[0]
			break
		}
	}
	if err, ok := f.errByCluster[cluster]; ok {
		return nil, err
	}
	return &ec2.DescribeInstancesOutput{Reservations: f.reservationsByCluster[cluster]}, nil
}

// instanceReservation builds the [Reservation{Instance{InstanceId}}]
// shape the EC2 SDK returns for the test fakes.
func instanceReservation(ids ...string) []ec2types.Reservation {
	res := ec2types.Reservation{}
	for _, id := range ids {
		res.Instances = append(res.Instances, ec2types.Instance{InstanceId: aws.String(id)})
	}
	return []ec2types.Reservation{res}
}

func TestListEKSNodeInstances_ScopedByProjectAndClusterTag(t *testing.T) {
	t.Parallel()
	// Two clusters; only foo carries the matching Project tag, so
	// filterEKSClustersByProjectTag prunes bar before the EC2 fan-out
	// even gets a chance to ask. The EC2 fake then returns foo's two
	// node instances scoped by the eks:cluster-name=foo tag.
	eksFake := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{Clusters: []string{"foo", "bar"}},
		describeByName: map[string]*eks.DescribeClusterOutput{
			"foo": {Cluster: &ekstypes.Cluster{
				Name: aws.String("foo"),
				Tags: map[string]string{"Project": "my-stack"},
			}},
			"bar": {Cluster: &ekstypes.Cluster{
				Name: aws.String("bar"),
				Tags: map[string]string{"Project": "other"},
			}},
		},
	}
	ec2Fake := &fakeEC2InstancesClient{
		reservationsByCluster: map[string][]ec2types.Reservation{
			"foo": instanceReservation("i-aaaa", "i-bbbb"),
			"bar": instanceReservation("i-cccc"), // must NOT appear: cluster pruned upstream
		},
	}

	got, err := listEKSNodeInstances(context.Background(), eksFake, ec2Fake, "my-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"i-aaaa", "i-bbbb"}, got)

	// Filter-shape assertion: every EC2 call must carry both
	// tag:eks:cluster-name AND tag:Project so the EC2 API enforces the
	// project scope server-side (defense in depth — the EKS-side
	// project filter could be bypassed if a cluster is mis-tagged).
	require.Len(t, ec2Fake.calls, 1, "bar must be pruned upstream so EC2 is only called once")
	filters := ec2Fake.calls[0].Filters
	require.Len(t, filters, 2)
	assert.Equal(t, "tag:eks:cluster-name", aws.ToString(filters[0].Name))
	assert.Equal(t, []string{"foo"}, filters[0].Values)
	assert.Equal(t, "tag:Project", aws.ToString(filters[1].Name))
	assert.Equal(t, []string{"my-stack"}, filters[1].Values)
}

func TestListEKSNodeInstances_EmptyProjectFansOutWithoutProjectFilter(t *testing.T) {
	t.Parallel()
	// Empty project: every cluster passes the EKS-side filter, and the
	// EC2 fan-out only carries the tag:eks:cluster-name filter (no
	// project clause). Mirrors the contract used by every other
	// per-service inspector — empty filter means "everything visible
	// to the credentials".
	eksFake := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{Clusters: []string{"foo"}},
	}
	ec2Fake := &fakeEC2InstancesClient{
		reservationsByCluster: map[string][]ec2types.Reservation{
			"foo": instanceReservation("i-1234"),
		},
	}
	got, err := listEKSNodeInstances(context.Background(), eksFake, ec2Fake, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"i-1234"}, got)

	require.Len(t, ec2Fake.calls, 1)
	filters := ec2Fake.calls[0].Filters
	require.Len(t, filters, 1, "no project → no tag:Project filter")
	assert.Equal(t, "tag:eks:cluster-name", aws.ToString(filters[0].Name))
}

func TestListEKSNodeInstances_PerClusterErrorLogsAndSkips(t *testing.T) {
	t.Parallel()
	// Two project-matched clusters; EC2 denies one. The helper logs
	// and continues — partial result beats empty when one cluster has
	// an IAM denial or throttle, matching every other tag-fan-out
	// helper in this package.
	eksFake := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{Clusters: []string{"foo", "bar"}},
		describeByName: map[string]*eks.DescribeClusterOutput{
			"foo": {Cluster: &ekstypes.Cluster{
				Name: aws.String("foo"),
				Tags: map[string]string{"Project": "my-stack"},
			}},
			"bar": {Cluster: &ekstypes.Cluster{
				Name: aws.String("bar"),
				Tags: map[string]string{"Project": "my-stack"},
			}},
		},
	}
	ec2Fake := &fakeEC2InstancesClient{
		reservationsByCluster: map[string][]ec2types.Reservation{
			"foo": instanceReservation("i-aaaa"),
		},
		errByCluster: map[string]error{
			"bar": errors.New("denied"),
		},
	}

	got, err := listEKSNodeInstances(context.Background(), eksFake, ec2Fake, "my-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"i-aaaa"}, got)
}

func TestListEKSNodeInstances_DedupesInstanceIDsAcrossClusters(t *testing.T) {
	t.Parallel()
	// Same instance reported under two clusters. Doesn't happen in
	// practice (an EC2 instance carries exactly one
	// eks:cluster-name=...) but pin the contract so a future refactor
	// that, e.g., joins the result of two queries doesn't
	// double-count.
	eksFake := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{Clusters: []string{"foo", "bar"}},
		describeByName: map[string]*eks.DescribeClusterOutput{
			"foo": {Cluster: &ekstypes.Cluster{Name: aws.String("foo"), Tags: map[string]string{"Project": "my-stack"}}},
			"bar": {Cluster: &ekstypes.Cluster{Name: aws.String("bar"), Tags: map[string]string{"Project": "my-stack"}}},
		},
	}
	ec2Fake := &fakeEC2InstancesClient{
		reservationsByCluster: map[string][]ec2types.Reservation{
			"foo": instanceReservation("i-shared", "i-foo-only"),
			"bar": instanceReservation("i-shared", "i-bar-only"),
		},
	}
	got, err := listEKSNodeInstances(context.Background(), eksFake, ec2Fake, "my-stack")
	require.NoError(t, err)
	assert.Equal(t, []string{"i-shared", "i-foo-only", "i-bar-only"}, got)
}

func TestListEKSNodeInstances_NoClustersReturnsEmpty(t *testing.T) {
	t.Parallel()
	// filterEKSClustersByProjectTag returning an empty list short-
	// circuits the fan-out — the helper must not call EC2 once.
	eksFake := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{},
	}
	ec2Fake := &fakeEC2InstancesClient{}

	got, err := listEKSNodeInstances(context.Background(), eksFake, ec2Fake, "my-stack")
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, ec2Fake.calls, "no clusters → no EC2 calls")
}

func TestListEKSNodeInstances_ListClustersErrorPropagates(t *testing.T) {
	t.Parallel()
	// EKS ListClusters failure is fatal — there's nothing to fan out
	// from and the caller needs to know the discovery side broke.
	// This mirrors filterEKSClustersByProjectTag's contract; we just
	// confirm listEKSNodeInstances propagates instead of swallowing.
	eksFake := &fakeEKSClient{
		listClustersErr: errors.New("denied"),
	}
	_, err := listEKSNodeInstances(context.Background(), eksFake, &fakeEC2InstancesClient{}, "")
	require.Error(t, err)
	assert.ErrorContains(t, err, "ListClusters")
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

// --- Empty-state JSON-shape pins per #256 ---
//
// These complement the existing `_NoArns/_NoMatch` short-circuit tests
// that use `assert.Empty` (which accepts both nil and []). The pins
// here assert the JSON wire shape is `[]` not `null`, which is what
// reliable's panel renderer gates on.

func TestFilterLambdaFunctionsByProjectTag_NoResults_EmptySlice(t *testing.T) {
	t.Parallel()
	client := &fakeLambdaClient{listFunctionsOut: &lambda.ListFunctionsOutput{}}
	got, err := filterLambdaFunctionsByProjectTag(context.Background(), client, "any-project")
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Lambda list-functions must marshal as [] not null (#256)")
}

func TestFilterECSClustersByProjectTag_NoResults_EmptySlice(t *testing.T) {
	t.Parallel()
	client := &fakeECSClient{
		listClustersOut: &ecs.ListClustersOutput{
			ClusterArns: []string{"arn:aws:ecs:us-east-1:111:cluster/foo"},
		},
		describeClustersOut: &ecs.DescribeClustersOutput{
			Clusters: []ecstypes.Cluster{
				{
					ClusterArn:  aws.String("arn:aws:ecs:us-east-1:111:cluster/foo"),
					ClusterName: aws.String("foo"),
					Tags:        []ecstypes.Tag{{Key: aws.String("Project"), Value: aws.String("other")}},
				},
			},
		},
	}
	got, err := filterECSClustersByProjectTag(context.Background(), client, "no-such-project")
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"no-match ECS list-clusters must marshal as [] not null (#256)")
}

func TestDescribeECSServicesAcrossClusters_NoClusters_EmptySlice(t *testing.T) {
	t.Parallel()
	// No matching clusters → no services → all := []ecstypes.Service{}.
	client := &fakeECSClient{
		listClustersOut: &ecs.ListClustersOutput{}, // zero ARNs
	}
	got, err := describeECSServicesAcrossClusters(context.Background(), client, "any-project")
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty ECS describe-services must marshal as [] not null (#256)")
}

func TestFilterEKSClustersByProjectTag_NoResults_EmptySlice(t *testing.T) {
	t.Parallel()
	client := &fakeEKSClient{listClustersOut: &eks.ListClustersOutput{}}
	got, err := filterEKSClustersByProjectTag(context.Background(), client, "any-project")
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty EKS list-clusters must marshal as [] not null (#256)")
}

func TestListEKSNodeInstances_NoClusters_EmptySlice(t *testing.T) {
	t.Parallel()
	// No EKS clusters in this project → no node instances → []string{}.
	eksFake := &fakeEKSClient{listClustersOut: &eks.ListClustersOutput{}}
	ec2Fake := &fakeEC2InstancesClient{}
	got, err := listEKSNodeInstances(context.Background(), eksFake, ec2Fake, "any-project")
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty EKS list-nodes must marshal as [] not null (#256)")
}

// TestEnrichEC2WithConnectURLs_NoReservations_EmptySlice pins the
// pure-transform site at aws/connect_info.go:33. enrichEC2WithConnectURLs
// is called with a `[]ec2types.Reservation` literal — no fake required.
func TestEnrichEC2WithConnectURLs_NoReservations_EmptySlice(t *testing.T) {
	t.Parallel()
	got := enrichEC2WithConnectURLs("us-east-1", []ec2types.Reservation{})
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty enrichEC2WithConnectURLs must marshal as [] not null (#256)")
}
