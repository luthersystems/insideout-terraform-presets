// Tests for the config fan-out the list actions do so the config
// extractors can compare design and deployed values (#927): KMS
// ListKeys, SQS GetQueueAttributes (describe-queues), DynamoDB
// DescribeTable (describe-tables), ECS Service Connect
// (describe-clusters), EKS
// DescribeCluster + DescribeNodegroup (describe-clusters), WAF GetWebACL
// and API Gateway custom domains.
//
// Each test checks three things the fan-out promises: the extra fields
// appear, a failed describe call degrades that resource to identity-only
// instead of failing the list, and the result marshals as a JSON array
// (never null, #255).

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigatewayv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func marshalRows(t *testing.T, v any) []map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(b, &rows))
	return rows
}

func requireEmptyJSONArray(t *testing.T, v any) {
	t.Helper()
	require.NotNil(t, v)
	b, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

// --- KMS ---

// fakeKMSPerKey answers DescribeKey and ListResourceTags per key id so a
// test can mix project keys, other projects' keys and AWS-managed keys.
type fakeKMSPerKey struct {
	aliases   []kmstypes.AliasListEntry
	keys      []string
	keysErr   error
	managers  map[string]kmstypes.KeyManagerType
	projects  map[string]string
	keysCalls int
	descCalls map[string]int
}

func (f *fakeKMSPerKey) ListKeys(_ context.Context, _ *kms.ListKeysInput, _ ...func(*kms.Options)) (*kms.ListKeysOutput, error) {
	f.keysCalls++
	if f.keysErr != nil {
		return nil, f.keysErr
	}
	out := &kms.ListKeysOutput{}
	for _, k := range f.keys {
		out.Keys = append(out.Keys, kmstypes.KeyListEntry{KeyId: aws.String(k)})
	}
	return out, nil
}

func (f *fakeKMSPerKey) ListAliases(_ context.Context, _ *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	return &kms.ListAliasesOutput{Aliases: f.aliases}, nil
}

func (f *fakeKMSPerKey) DescribeKey(_ context.Context, in *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	id := aws.ToString(in.KeyId)
	if f.descCalls == nil {
		f.descCalls = map[string]int{}
	}
	f.descCalls[id]++
	return &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{KeyId: in.KeyId, KeyManager: f.managers[id]}}, nil
}

func (f *fakeKMSPerKey) GetKeyRotationStatus(_ context.Context, _ *kms.GetKeyRotationStatusInput, _ ...func(*kms.Options)) (*kms.GetKeyRotationStatusOutput, error) {
	return &kms.GetKeyRotationStatusOutput{}, nil
}

func (f *fakeKMSPerKey) ListResourceTags(_ context.Context, in *kms.ListResourceTagsInput, _ ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error) {
	p := f.projects[aws.ToString(in.KeyId)]
	if p == "" {
		return &kms.ListResourceTagsOutput{}, nil
	}
	return &kms.ListResourceTagsOutput{Tags: []kmstypes.Tag{{TagKey: aws.String("Project"), TagValue: aws.String(p)}}}, nil
}

func newKMSFixture() *fakeKMSPerKey {
	return &fakeKMSPerKey{
		aliases: []kmstypes.AliasListEntry{
			{AliasName: aws.String("alias/my-stack-a"), TargetKeyId: aws.String("k1")},
			{AliasName: aws.String("alias/my-stack-a2"), TargetKeyId: aws.String("k1")},
			{AliasName: aws.String("alias/other"), TargetKeyId: aws.String("k3")},
			{AliasName: aws.String("alias/aws/s3")},
		},
		keys: []string{"k1", "k2", "k3", "k4"},
		managers: map[string]kmstypes.KeyManagerType{
			"k1": kmstypes.KeyManagerTypeCustomer,
			"k2": kmstypes.KeyManagerTypeCustomer,
			"k3": kmstypes.KeyManagerTypeCustomer,
			"k4": kmstypes.KeyManagerTypeAws,
		},
		projects: map[string]string{"k1": "my-stack", "k2": "my-stack", "k3": "other"},
	}
}

func TestListProjectKMSKeys_AddsUnaliasedProjectKeys(t *testing.T) {
	t.Parallel()
	client := newKMSFixture()
	got, err := listProjectKMSKeys(context.Background(), client, "my-stack")
	require.NoError(t, err)

	rows := marshalRows(t, got)
	require.Len(t, rows, 3, "two aliases of k1 plus a key-only row for unaliased k2")
	assert.Equal(t, "alias/my-stack-a", rows[0]["AliasName"])
	_, hasKeyOnly := rows[0]["KeyOnly"]
	assert.False(t, hasKeyOnly, "alias rows keep their pre-#927 JSON shape")
	assert.Equal(t, "k2", rows[2]["TargetKeyId"])
	assert.Equal(t, true, rows[2]["KeyOnly"])

	// The alias pass already checked k1 and k3; the ListKeys pass reuses
	// those answers and only checks the keys it has not seen.
	assert.Equal(t, map[string]int{"k1": 1, "k2": 1, "k3": 1, "k4": 1}, client.descCalls)
}

// TestInspectKMS_ListAliasesUnchanged pins that list-aliases still
// returns only the project alias rows, as before #927: no ListKeys call
// and no KeyOnly row without an AliasName. The key-count rows live on
// list-keys, the action the aws_kms drift extractor reads.
func TestInspectKMS_ListAliasesUnchanged(t *testing.T) {
	t.Parallel()
	client := newKMSFixture()
	got, err := inspectKMSWithClient(context.Background(), client, "list-aliases", "my-stack")
	require.NoError(t, err)
	require.IsType(t, []kmstypes.AliasListEntry{}, got, "the pre-#927 SDK alias rows")
	assert.Equal(t, 0, client.keysCalls, "list-aliases makes no ListKeys pass")
	rows := marshalRows(t, got)
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.NotEmpty(t, r["AliasName"], "every list-aliases row is an alias")
		assert.NotContains(t, r, "KeyOnly")
	}

	keys, err := inspectKMSWithClient(context.Background(), newKMSFixture(), "list-keys", "my-stack")
	require.NoError(t, err)
	keyRows := marshalRows(t, keys)
	require.Len(t, keyRows, 3, "list-keys adds the unaliased project key")
	assert.Equal(t, true, keyRows[2]["KeyOnly"])
}

func TestListProjectKMSKeys_ListKeysErrorDegradesToAliases(t *testing.T) {
	t.Parallel()
	client := newKMSFixture()
	client.keysErr = errors.New("AccessDenied: kms:ListKeys")
	got, err := listProjectKMSKeys(context.Background(), client, "my-stack")
	require.NoError(t, err, "a ListKeys failure must not fail the inspection")
	require.Len(t, got, 2)
	for _, e := range got {
		assert.False(t, e.KeyOnly)
	}
}

func TestListProjectKMSKeys_NoProjectSkipsListKeys(t *testing.T) {
	t.Parallel()
	client := newKMSFixture()
	got, err := listProjectKMSKeys(context.Background(), client, "")
	require.NoError(t, err)
	assert.Len(t, got, 4, "demo path returns the unscoped alias list unchanged")
	assert.Zero(t, client.keysCalls)
}

func TestListProjectKMSKeys_StopsAtCheckLimit(t *testing.T) {
	t.Parallel()
	client := &fakeKMSPerKey{managers: map[string]kmstypes.KeyManagerType{}, projects: map[string]string{}}
	for i := 0; i < kmsUnaliasedKeyCheckLimit+5; i++ {
		id := fmt.Sprintf("k%d", i)
		client.keys = append(client.keys, id)
		client.managers[id] = kmstypes.KeyManagerTypeCustomer
	}
	_, err := listProjectKMSKeys(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Len(t, client.descCalls, kmsUnaliasedKeyCheckLimit)
}

func TestListProjectKMSKeys_NoKeys_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := listProjectKMSKeys(context.Background(), &fakeKMSPerKey{}, "my-stack")
	require.NoError(t, err)
	requireEmptyJSONArray(t, got)
}

// --- SQS ---

type fakeSQSClient struct {
	urls       []string
	attrs      map[string]map[string]string
	attrErrFor map[string]error
	attrCalls  int
}

func (f *fakeSQSClient) ListQueues(_ context.Context, _ *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	return &sqs.ListQueuesOutput{QueueUrls: f.urls}, nil
}

func (f *fakeSQSClient) GetQueueAttributes(_ context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	f.attrCalls++
	u := aws.ToString(in.QueueUrl)
	if err := f.attrErrFor[u]; err != nil {
		return nil, err
	}
	return &sqs.GetQueueAttributesOutput{Attributes: f.attrs[u]}, nil
}

func TestDescribeSQSQueues_ReadsConfigAttributes(t *testing.T) {
	t.Parallel()
	const std = "https://sqs.us-east-1.amazonaws.com/111/my-stack-q"
	const fifo = "https://sqs.us-east-1.amazonaws.com/111/my-stack-q.fifo"
	const broken = "https://sqs.us-east-1.amazonaws.com/111/my-stack-broken"
	client := &fakeSQSClient{
		urls: []string{std, fifo, broken},
		attrs: map[string]map[string]string{
			std:  {"VisibilityTimeout": "600"},
			fifo: {"VisibilityTimeout": "30", "FifoQueue": "true"},
		},
		attrErrFor: map[string]error{broken: errors.New("throttled")},
	}
	got, err := describeSQSQueues(context.Background(), client, "my-stack")
	require.NoError(t, err, "a GetQueueAttributes failure must not fail the inspection")

	rows := marshalRows(t, got)
	require.Len(t, rows, 3)
	assert.Equal(t, std, rows[0]["QueueUrl"])
	assert.InDelta(t, 600, rows[0]["VisibilityTimeout"], 0)
	assert.Equal(t, false, rows[0]["FifoQueue"], "a standard queue has no FifoQueue attribute")
	assert.Equal(t, true, rows[1]["FifoQueue"])
	assert.Equal(t, map[string]any{"QueueUrl": broken}, rows[2], "failed describe degrades to the URL only")
}

func TestDescribeSQSQueues_BoundsFanOut(t *testing.T) {
	t.Parallel()
	client := &fakeSQSClient{}
	for i := 0; i < configFanOutLimit+3; i++ {
		client.urls = append(client.urls, fmt.Sprintf("https://sqs/111/q%d", i))
	}
	got, err := describeSQSQueues(context.Background(), client, "")
	require.NoError(t, err)
	assert.Len(t, got, configFanOutLimit+3, "every queue is listed")
	assert.Equal(t, configFanOutLimit, client.attrCalls, "only the first configFanOutLimit are described")
}

func TestDescribeSQSQueues_NoQueues_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := describeSQSQueues(context.Background(), &fakeSQSClient{}, "my-stack")
	require.NoError(t, err)
	requireEmptyJSONArray(t, got)
}

// --- DynamoDB ---

func TestDescribeDynamoDBTables_ReadsConfig(t *testing.T) {
	t.Parallel()
	client := &fakeDynamoDBClient{
		describeByName: map[string]*dynamodbtypes.TableDescription{
			"t1": {
				BillingModeSummary:    &dynamodbtypes.BillingModeSummary{BillingMode: dynamodbtypes.BillingModePayPerRequest},
				ProvisionedThroughput: &dynamodbtypes.ProvisionedThroughputDescription{ReadCapacityUnits: aws.Int64(0)},
				SSEDescription:        &dynamodbtypes.SSEDescription{Status: dynamodbtypes.SSEStatusEnabled, SSEType: dynamodbtypes.SSETypeKms},
			},
		},
		describeErrFor: map[string]error{"t2": errors.New("AccessDenied")},
	}
	got := describeDynamoDBTables(context.Background(), client, []string{"t1", "t2"})

	rows := marshalRows(t, got)
	require.Len(t, rows, 2)
	assert.Equal(t, "t1", rows[0]["TableName"])
	assert.Equal(t, "PAY_PER_REQUEST", rows[0]["BillingModeSummary"].(map[string]any)["BillingMode"])
	assert.Contains(t, rows[0], "ProvisionedThroughput")
	assert.Equal(t, "ENABLED", rows[0]["SSEDescription"].(map[string]any)["Status"])
	assert.Equal(t, map[string]any{"TableName": "t2"}, rows[1], "failed describe degrades to the name only")
}

func TestDescribeDynamoDBTables_BoundsFanOut(t *testing.T) {
	t.Parallel()
	client := &fakeDynamoDBClient{}
	names := make([]string, configFanOutLimit+2)
	for i := range names {
		names[i] = fmt.Sprintf("t%d", i)
	}
	got := describeDynamoDBTables(context.Background(), client, names)
	assert.Len(t, got, len(names))
	assert.Equal(t, configFanOutLimit, client.describeCalls)
}

func TestDescribeDynamoDBTables_NoTables_EmptySlice(t *testing.T) {
	t.Parallel()
	requireEmptyJSONArray(t, describeDynamoDBTables(context.Background(), &fakeDynamoDBClient{}, []string{}))
}

// --- ECS ---

// fakeECSServiceConnect routes ListServices / DescribeServices per
// cluster ARN and records the DescribeClusters Include set.
type fakeECSServiceConnect struct {
	clusters        []ecstypes.Cluster
	include         []ecstypes.ClusterField
	servicesByARN   map[string][]ecstypes.Service
	listErrFor      map[string]error
	listServiceCall int
}

func (f *fakeECSServiceConnect) ListClusters(_ context.Context, _ *ecs.ListClustersInput, _ ...func(*ecs.Options)) (*ecs.ListClustersOutput, error) {
	out := &ecs.ListClustersOutput{}
	for _, c := range f.clusters {
		out.ClusterArns = append(out.ClusterArns, aws.ToString(c.ClusterArn))
	}
	return out, nil
}

func (f *fakeECSServiceConnect) DescribeClusters(_ context.Context, in *ecs.DescribeClustersInput, _ ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error) {
	f.include = in.Include
	return &ecs.DescribeClustersOutput{Clusters: f.clusters}, nil
}

func (f *fakeECSServiceConnect) ListServices(_ context.Context, in *ecs.ListServicesInput, _ ...func(*ecs.Options)) (*ecs.ListServicesOutput, error) {
	f.listServiceCall++
	arn := aws.ToString(in.Cluster)
	if err := f.listErrFor[arn]; err != nil {
		return nil, err
	}
	out := &ecs.ListServicesOutput{}
	for _, s := range f.servicesByARN[arn] {
		out.ServiceArns = append(out.ServiceArns, aws.ToString(s.ServiceArn))
	}
	return out, nil
}

func (f *fakeECSServiceConnect) DescribeServices(_ context.Context, in *ecs.DescribeServicesInput, _ ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	return &ecs.DescribeServicesOutput{Services: f.servicesByARN[aws.ToString(in.Cluster)]}, nil
}

func ecsSvc(arn string, scOn bool) ecstypes.Service {
	return ecstypes.Service{
		ServiceArn: aws.String(arn),
		Deployments: []ecstypes.Deployment{
			{Status: aws.String("ACTIVE"), ServiceConnectConfiguration: &ecstypes.ServiceConnectConfiguration{Enabled: !scOn}},
			{Status: aws.String("PRIMARY"), ServiceConnectConfiguration: &ecstypes.ServiceConnectConfiguration{Enabled: scOn}},
		},
	}
}

func TestFilterECSClustersByProjectTag_IncludesSettings(t *testing.T) {
	t.Parallel()
	client := &fakeECSServiceConnect{clusters: []ecstypes.Cluster{{ClusterArn: aws.String("arn:c1")}}}
	_, err := filterECSClustersByProjectTag(context.Background(), client, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []ecstypes.ClusterField{ecstypes.ClusterFieldTags, ecstypes.ClusterFieldSettings}, client.include,
		"SETTINGS carries the containerInsights setting at no extra call")
}

func TestEnrichECSServiceConnect(t *testing.T) {
	t.Parallel()
	clusters := []ecstypes.Cluster{
		{ClusterArn: aws.String("arn:on"), ClusterName: aws.String("on")},
		{ClusterArn: aws.String("arn:off"), ClusterName: aws.String("off")},
		{ClusterArn: aws.String("arn:empty"), ClusterName: aws.String("empty")},
		{ClusterArn: aws.String("arn:default"), ClusterName: aws.String("default"),
			ServiceConnectDefaults: &ecstypes.ClusterServiceConnectDefaults{Namespace: aws.String("arn:ns")}},
		{ClusterArn: aws.String("arn:broken"), ClusterName: aws.String("broken")},
	}
	client := &fakeECSServiceConnect{
		servicesByARN: map[string][]ecstypes.Service{
			"arn:on":  {ecsSvc("arn:svc1", false), ecsSvc("arn:svc2", true)},
			"arn:off": {ecsSvc("arn:svc3", false)},
		},
		listErrFor: map[string]error{"arn:broken": errors.New("throttled")},
	}
	got := enrichECSServiceConnect(context.Background(), client, clusters)

	rows := marshalRows(t, got)
	require.Len(t, rows, 5)
	assert.Equal(t, "on", rows[0]["ClusterName"], "the embedded SDK cluster keeps its JSON shape")
	assert.Equal(t, true, rows[0]["ServiceConnectEnabled"])
	assert.Equal(t, false, rows[1]["ServiceConnectEnabled"], "only the PRIMARY deployment counts")
	assert.NotContains(t, rows[2], "ServiceConnectEnabled", "no services: unknown, not false")
	assert.Equal(t, true, rows[3]["ServiceConnectEnabled"])
	assert.NotContains(t, rows[4], "ServiceConnectEnabled", "a failed call leaves the field unknown")
	assert.Equal(t, 4, client.listServiceCall, "a cluster with a default namespace needs no service calls")
}

// TestInspectECS_ListClustersStaysCheap pins that list-clusters (the
// ECS panel default, also read by the metrics discovery) makes no
// per-cluster service calls and returns the SDK clusters as before #927.
// The Service Connect sampling runs only for describe-clusters.
func TestInspectECS_ListClustersStaysCheap(t *testing.T) {
	t.Parallel()
	newClient := func() *fakeECSServiceConnect {
		return &fakeECSServiceConnect{
			clusters: []ecstypes.Cluster{{ClusterArn: aws.String("arn:on"), ClusterName: aws.String("on")}},
			servicesByARN: map[string][]ecstypes.Service{
				"arn:on": {ecsSvc("arn:svc1", true)},
			},
		}
	}

	client := newClient()
	got, err := inspectECSWithClient(context.Background(), client, "list-clusters", "")
	require.NoError(t, err)
	require.IsType(t, []ecstypes.Cluster{}, got, "list-clusters keeps the bare SDK cluster rows")
	assert.Equal(t, 0, client.listServiceCall, "list-clusters makes no ListServices call")
	rows := marshalRows(t, got)
	require.Len(t, rows, 1)
	assert.NotContains(t, rows[0], "ServiceConnectEnabled")

	client = newClient()
	got, err = inspectECSWithClient(context.Background(), client, "describe-clusters", "")
	require.NoError(t, err)
	assert.Equal(t, 1, client.listServiceCall)
	rows = marshalRows(t, got)
	require.Len(t, rows, 1)
	assert.Equal(t, "on", rows[0]["ClusterName"])
	assert.Equal(t, true, rows[0]["ServiceConnectEnabled"])
}

func TestEnrichECSServiceConnect_NoClusters_EmptySlice(t *testing.T) {
	t.Parallel()
	requireEmptyJSONArray(t, enrichECSServiceConnect(context.Background(), &fakeECSServiceConnect{}, []ecstypes.Cluster{}))
}

// --- EKS ---

func eksClusterOut(project string, public bool) *eks.DescribeClusterOutput {
	return &eks.DescribeClusterOutput{Cluster: &ekstypes.Cluster{
		Version: aws.String("1.31"),
		Tags:    map[string]string{"Project": project},
		ResourcesVpcConfig: &ekstypes.VpcConfigResponse{
			EndpointPublicAccess:  public,
			EndpointPrivateAccess: true,
		},
	}}
}

func TestDescribeEKSClusters_ReusesTagCheckAndReadsNodeGroups(t *testing.T) {
	t.Parallel()
	client := &fakeEKSClient{
		listClustersOut: &eks.ListClustersOutput{Clusters: []string{"mine", "theirs"}},
		describeByName: map[string]*eks.DescribeClusterOutput{
			"mine":   eksClusterOut("my-stack", false),
			"theirs": eksClusterOut("other", true),
		},
		nodegroupsByName: map[string][]string{"mine": {"ng1", "ng-broken"}},
		nodegroupByName: map[string]*ekstypes.Nodegroup{
			"ng1": {
				InstanceTypes: []string{"m5.large"},
				ScalingConfig: &ekstypes.NodegroupScalingConfig{DesiredSize: aws.Int32(2), MinSize: aws.Int32(1), MaxSize: aws.Int32(3)},
			},
		},
	}
	matches, err := filterEKSClustersByProjectTagDetailed(context.Background(), client, "my-stack")
	require.NoError(t, err)
	got := describeEKSClusters(context.Background(), client, matches)
	assert.Equal(t, 2, client.describeCalls, "the tag check's DescribeCluster answer is reused, not fetched again")

	rows := marshalRows(t, got)
	require.Len(t, rows, 1)
	assert.Equal(t, "mine", rows[0]["Name"])
	assert.Equal(t, "1.31", rows[0]["Version"])
	assert.Equal(t, false, rows[0]["EndpointPublicAccess"])
	ngs := rows[0]["NodeGroups"].([]any)
	require.Len(t, ngs, 1, "the node group whose describe returned nothing is dropped")
	ng := ngs[0].(map[string]any)
	assert.Equal(t, []any{"m5.large"}, ng["InstanceTypes"])
	assert.InDelta(t, 2, ng["DesiredSize"], 0)
}

func TestDescribeEKSClusters_NoProjectDescribesAndDegrades(t *testing.T) {
	t.Parallel()
	client := &fakeEKSClient{
		describeClusterErr: errors.New("AccessDenied"),
		listNodegroupsErr:  errors.New("AccessDenied"),
	}
	got := describeEKSClusters(context.Background(), client, []eksClusterMatch{{name: "c1"}})
	rows := marshalRows(t, got)
	assert.Equal(t, []map[string]any{{"Name": "c1"}}, rows, "failed describes degrade to the name only")
	assert.Equal(t, 1, client.describeCalls, "the no-project path has no tag-check answer to reuse")
}

func TestDescribeEKSClusters_NoClusters_EmptySlice(t *testing.T) {
	t.Parallel()
	requireEmptyJSONArray(t, describeEKSClusters(context.Background(), &fakeEKSClient{}, []eksClusterMatch{}))
}

// --- WAF ---

type fakeWAFClient struct {
	acls     map[string]*wafv2types.WebACL
	errFor   map[string]error
	gotScope []wafv2types.Scope
}

func (f *fakeWAFClient) ListWebACLs(_ context.Context, _ *wafv2.ListWebACLsInput, _ ...func(*wafv2.Options)) (*wafv2.ListWebACLsOutput, error) {
	return &wafv2.ListWebACLsOutput{}, nil
}

func (f *fakeWAFClient) ListTagsForResource(_ context.Context, _ *wafv2.ListTagsForResourceInput, _ ...func(*wafv2.Options)) (*wafv2.ListTagsForResourceOutput, error) {
	return &wafv2.ListTagsForResourceOutput{}, nil
}

func (f *fakeWAFClient) GetWebACL(_ context.Context, in *wafv2.GetWebACLInput, _ ...func(*wafv2.Options)) (*wafv2.GetWebACLOutput, error) {
	f.gotScope = append(f.gotScope, in.Scope)
	id := aws.ToString(in.Id)
	if err := f.errFor[id]; err != nil {
		return nil, err
	}
	return &wafv2.GetWebACLOutput{WebACL: f.acls[id]}, nil
}

func TestDescribeWAFWebACLs(t *testing.T) {
	t.Parallel()
	client := &fakeWAFClient{
		acls: map[string]*wafv2types.WebACL{
			"a1": {
				DefaultAction: &wafv2types.DefaultAction{Block: &wafv2types.BlockAction{}},
				Rules: []wafv2types.Rule{
					{Name: aws.String("second"), Priority: 2},
					{Name: aws.String("first"), Priority: 1},
				},
			},
		},
		errFor: map[string]error{"a2": errors.New("WAFNonexistentItemException")},
	}
	summaries := []wafv2types.WebACLSummary{
		{Id: aws.String("a1"), Name: aws.String("acl-1")},
		{Id: aws.String("a2"), Name: aws.String("acl-2")},
	}
	got := describeWAFWebACLs(context.Background(), client, wafv2types.ScopeCloudfront, summaries)
	assert.Equal(t, []wafv2types.Scope{wafv2types.ScopeCloudfront, wafv2types.ScopeCloudfront}, client.gotScope)

	rows := marshalRows(t, got)
	require.Len(t, rows, 2)
	assert.Equal(t, "acl-1", rows[0]["Name"], "the embedded summary keeps its JSON shape")
	assert.Equal(t, "CLOUDFRONT", rows[0]["Scope"])
	assert.Equal(t, "BLOCK", rows[0]["DefaultAction"])
	assert.Equal(t, []any{"first", "second"}, rows[0]["RuleNames"], "rules are listed in priority order")
	assert.InDelta(t, 2, rows[0]["RuleCount"], 0)
	assert.NotContains(t, rows[1], "DefaultAction", "a failed GetWebACL degrades to the summary")
	assert.Equal(t, "acl-2", rows[1]["Name"])
}

func TestDescribeWAFWebACLs_NoACLs_EmptySlice(t *testing.T) {
	t.Parallel()
	requireEmptyJSONArray(t, describeWAFWebACLs(context.Background(), &fakeWAFClient{}, wafv2types.ScopeRegional, nil))
}

// --- API Gateway ---

type fakeAPIGatewayClient struct {
	apis        []apigatewayv2types.Api
	domains     []apigatewayv2types.DomainName
	domainsErr  error
	mappings    map[string][]apigatewayv2types.ApiMapping
	mappingCall []string
}

func (f *fakeAPIGatewayClient) GetApis(_ context.Context, _ *apigatewayv2.GetApisInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error) {
	return &apigatewayv2.GetApisOutput{Items: f.apis}, nil
}

func (f *fakeAPIGatewayClient) GetDomainNames(_ context.Context, _ *apigatewayv2.GetDomainNamesInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetDomainNamesOutput, error) {
	if f.domainsErr != nil {
		return nil, f.domainsErr
	}
	return &apigatewayv2.GetDomainNamesOutput{Items: f.domains}, nil
}

func (f *fakeAPIGatewayClient) GetApiMappings(_ context.Context, in *apigatewayv2.GetApiMappingsInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApiMappingsOutput, error) {
	name := aws.ToString(in.DomainName)
	f.mappingCall = append(f.mappingCall, name)
	return &apigatewayv2.GetApiMappingsOutput{Items: f.mappings[name]}, nil
}

func newAPIGatewayFixture() *fakeAPIGatewayClient {
	tags := map[string]string{"Project": "my-stack"}
	return &fakeAPIGatewayClient{
		apis: []apigatewayv2types.Api{
			{ApiId: aws.String("api1"), Name: aws.String("mine"), ApiEndpoint: aws.String("https://api1.execute-api.us-east-1.amazonaws.com"), Tags: tags},
			{ApiId: aws.String("api2"), Name: aws.String("theirs"), Tags: map[string]string{"Project": "other"}},
		},
		domains: []apigatewayv2types.DomainName{
			{
				DomainName: aws.String("api.example.com"),
				Tags:       tags,
				DomainNameConfigurations: []apigatewayv2types.DomainNameConfiguration{
					{CertificateArn: aws.String("arn:aws:acm:us-east-1:111:certificate/abc")},
				},
			},
			{DomainName: aws.String("other.example.com"), Tags: map[string]string{"Project": "other"}},
		},
		mappings: map[string][]apigatewayv2types.ApiMapping{
			"api.example.com": {{ApiId: aws.String("api1")}},
		},
	}
}

func TestListAPIGatewayAPIs_AttachesCustomDomain(t *testing.T) {
	t.Parallel()
	client := newAPIGatewayFixture()
	got, err := listAPIGatewayAPIs(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "api1", got[0]["ApiId"])
	assert.Equal(t, "api.example.com", got[0]["CustomDomainName"])
	assert.Equal(t, "arn:aws:acm:us-east-1:111:certificate/abc", got[0]["CertificateArn"])
	assert.Equal(t, []string{"api.example.com"}, client.mappingCall, "other projects' domains are not looked up")
}

func TestListAPIGatewayAPIs_DomainErrorDegrades(t *testing.T) {
	t.Parallel()
	client := newAPIGatewayFixture()
	client.domainsErr = errors.New("AccessDenied: apigateway:GET")
	got, err := listAPIGatewayAPIs(context.Background(), client, "my-stack")
	require.NoError(t, err, "a GetDomainNames failure must not fail the inspection")
	require.Len(t, got, 1)
	assert.NotContains(t, got[0], "CustomDomainName")
}

func TestListAPIGatewayAPIs_NoAPIs_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := listAPIGatewayAPIs(context.Background(), &fakeAPIGatewayClient{}, "")
	require.NoError(t, err)
	requireEmptyJSONArray(t, got)
}
