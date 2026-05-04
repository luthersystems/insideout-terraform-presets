// Data-tier inspector tests. Covers the OpenSearch managed+AOSS union
// (the trickiest piece — a single "opensearch" service spans two SDKs),
// the DynamoDB ARN-from-name construction, and the ElastiCache shared
// generic helper.

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/aws-sdk-go-v2/service/opensearchserverless"
	aosstypes "github.com/aws/aws-sdk-go-v2/service/opensearchserverless/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- OpenSearch managed fake ---

type fakeOpenSearchAPI struct {
	listOut   *opensearch.ListDomainNamesOutput
	listErr   error
	descOut   *opensearch.DescribeDomainsOutput
	descErr   error
	tagsOut   *opensearch.ListTagsOutput
	tagsErr   error
	tagsCalls int
}

func (f *fakeOpenSearchAPI) ListDomainNames(_ context.Context, _ *opensearch.ListDomainNamesInput, _ ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut == nil {
		return &opensearch.ListDomainNamesOutput{}, nil
	}
	return f.listOut, nil
}

func (f *fakeOpenSearchAPI) DescribeDomains(_ context.Context, _ *opensearch.DescribeDomainsInput, _ ...func(*opensearch.Options)) (*opensearch.DescribeDomainsOutput, error) {
	if f.descErr != nil {
		return nil, f.descErr
	}
	if f.descOut == nil {
		return &opensearch.DescribeDomainsOutput{}, nil
	}
	return f.descOut, nil
}

func (f *fakeOpenSearchAPI) ListTags(_ context.Context, _ *opensearch.ListTagsInput, _ ...func(*opensearch.Options)) (*opensearch.ListTagsOutput, error) {
	f.tagsCalls++
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &opensearch.ListTagsOutput{}, nil
	}
	return f.tagsOut, nil
}

// --- AOSS fake ---

type fakeAOSSAPI struct {
	listOut *opensearchserverless.ListCollectionsOutput
	listErr error
	tagsOut *opensearchserverless.ListTagsForResourceOutput
	tagsErr error
}

func (f *fakeAOSSAPI) ListCollections(_ context.Context, _ *opensearchserverless.ListCollectionsInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.ListCollectionsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut == nil {
		return &opensearchserverless.ListCollectionsOutput{}, nil
	}
	return f.listOut, nil
}

func (f *fakeAOSSAPI) ListTagsForResource(_ context.Context, _ *opensearchserverless.ListTagsForResourceInput, _ ...func(*opensearchserverless.Options)) (*opensearchserverless.ListTagsForResourceOutput, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &opensearchserverless.ListTagsForResourceOutput{}, nil
	}
	return f.tagsOut, nil
}

func TestDiscoverOpenSearchManaged_Match(t *testing.T) {
	t.Parallel()
	managed := &fakeOpenSearchAPI{
		listOut: &opensearch.ListDomainNamesOutput{
			DomainNames: []opensearchtypes.DomainInfo{
				{DomainName: aws.String("d1")},
			},
		},
		descOut: &opensearch.DescribeDomainsOutput{
			DomainStatusList: []opensearchtypes.DomainStatus{
				{DomainName: aws.String("d1"), ARN: aws.String("arn:d1")},
			},
		},
		tagsOut: &opensearch.ListTagsOutput{
			TagList: []opensearchtypes.Tag{
				{Key: aws.String("Project"), Value: aws.String("my-stack")},
			},
		},
	}
	got, err := discoverOpenSearchManaged(context.Background(), managed, "my-stack")
	require.NoError(t, err)
	maps, ok := got.([]map[string]any)
	require.True(t, ok)
	assert.Len(t, maps, 1)
}

func TestDiscoverOpenSearchManaged_EmptyProjectReturnsRaw(t *testing.T) {
	t.Parallel()
	managed := &fakeOpenSearchAPI{
		listOut: &opensearch.ListDomainNamesOutput{
			DomainNames: []opensearchtypes.DomainInfo{{DomainName: aws.String("d1")}},
		},
		descOut: &opensearch.DescribeDomainsOutput{
			DomainStatusList: []opensearchtypes.DomainStatus{
				{DomainName: aws.String("d1"), ARN: aws.String("arn:d1")},
			},
		},
	}
	got, err := discoverOpenSearchManaged(context.Background(), managed, "")
	require.NoError(t, err)
	// Empty project returns the raw typed slice — preserves the InsideOut backend's
	// shape (descOut.DomainStatusList passed through).
	statuses, ok := got.([]opensearchtypes.DomainStatus)
	require.True(t, ok)
	assert.Len(t, statuses, 1)
	assert.Equal(t, 0, managed.tagsCalls, "empty project must skip the per-domain ListTags fan-out")
}

func TestDiscoverOpenSearchUnion_BothSidesContribute(t *testing.T) {
	t.Parallel()
	managed := &fakeOpenSearchAPI{
		listOut: &opensearch.ListDomainNamesOutput{
			DomainNames: []opensearchtypes.DomainInfo{{DomainName: aws.String("d1")}},
		},
		descOut: &opensearch.DescribeDomainsOutput{
			DomainStatusList: []opensearchtypes.DomainStatus{
				{DomainName: aws.String("d1"), ARN: aws.String("arn:d1")},
			},
		},
		tagsOut: &opensearch.ListTagsOutput{
			TagList: []opensearchtypes.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
		},
	}
	aoss := &fakeAOSSAPI{
		listOut: &opensearchserverless.ListCollectionsOutput{
			CollectionSummaries: []aosstypes.CollectionSummary{
				{Arn: aws.String("arn:c1"), Name: aws.String("c1")},
			},
		},
		tagsOut: &opensearchserverless.ListTagsForResourceOutput{
			Tags: []aosstypes.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
		},
	}
	got, err := discoverOpenSearchUnion(context.Background(), managed, aoss, "my-stack")
	require.NoError(t, err)
	maps, ok := got.([]map[string]any)
	require.True(t, ok)
	assert.Len(t, maps, 2, "union must include both managed-domain and AOSS-collection results")
}

func TestDiscoverOpenSearchUnion_AOSSAloneSurvivesManagedFailure(t *testing.T) {
	t.Parallel()
	// Managed errors → log+continue. AOSS still returns its result.
	// Mirrors the bug fix in the InsideOut backend #1036: AOSS-only deploys must not
	// be invisible to drift just because managed-side discovery hiccups.
	managed := &fakeOpenSearchAPI{listErr: errors.New("managed denied")}
	aoss := &fakeAOSSAPI{
		listOut: &opensearchserverless.ListCollectionsOutput{
			CollectionSummaries: []aosstypes.CollectionSummary{{Arn: aws.String("arn:c1")}},
		},
		tagsOut: &opensearchserverless.ListTagsForResourceOutput{
			Tags: []aosstypes.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
		},
	}
	got, err := discoverOpenSearchUnion(context.Background(), managed, aoss, "my-stack")
	require.NoError(t, err)
	maps, ok := got.([]map[string]any)
	require.True(t, ok)
	assert.Len(t, maps, 1, "AOSS half must still surface when managed half errors")
}

func TestDiscoverOpenSearchUnion_BothErrorSurfacesManagedError(t *testing.T) {
	t.Parallel()
	managed := &fakeOpenSearchAPI{listErr: errors.New("managed denied")}
	aoss := &fakeAOSSAPI{listErr: errors.New("aoss denied")}
	_, err := discoverOpenSearchUnion(context.Background(), managed, aoss, "my-stack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "managed denied")
}

// --- DynamoDB ---

type fakeDynamoDBClient struct {
	tablesOut *dynamodb.ListTablesOutput
	tablesErr error
	tagsOut   *dynamodb.ListTagsOfResourceOutput
	tagsErr   error
}

func (f *fakeDynamoDBClient) ListTables(_ context.Context, _ *dynamodb.ListTablesInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error) {
	if f.tablesErr != nil {
		return nil, f.tablesErr
	}
	if f.tablesOut == nil {
		return &dynamodb.ListTablesOutput{}, nil
	}
	return f.tablesOut, nil
}

func (f *fakeDynamoDBClient) ListTagsOfResource(_ context.Context, _ *dynamodb.ListTagsOfResourceInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &dynamodb.ListTagsOfResourceOutput{}, nil
	}
	return f.tagsOut, nil
}

type fakeSTSClient struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (f *fakeSTSClient) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func TestFilterDynamoDBTablesByProjectTag_EmptyProjectShortCircuits(t *testing.T) {
	t.Parallel()
	ddb := &fakeDynamoDBClient{
		tablesOut: &dynamodb.ListTablesOutput{TableNames: []string{"t1", "t2"}},
	}
	stsClient := &fakeSTSClient{} // never called
	got, err := filterDynamoDBTablesByProjectTag(context.Background(), ddb, stsClient, "us-east-1", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"t1", "t2"}, got)
}

func TestFilterDynamoDBTablesByProjectTag_Match(t *testing.T) {
	t.Parallel()
	ddb := &fakeDynamoDBClient{
		tablesOut: &dynamodb.ListTablesOutput{TableNames: []string{"t1"}},
		tagsOut: &dynamodb.ListTagsOfResourceOutput{
			Tags: []dynamodbtypes.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
		},
	}
	stsClient := &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")},
	}
	got, err := filterDynamoDBTablesByProjectTag(context.Background(), ddb, stsClient, "us-east-1", "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "t1", got[0])
}

func TestFilterDynamoDBTablesByProjectTag_STSErrorAborts(t *testing.T) {
	t.Parallel()
	// GetCallerIdentity error means we can't construct ARNs → abort
	// (caller can't see a silently-partial scope).
	ddb := &fakeDynamoDBClient{
		tablesOut: &dynamodb.ListTablesOutput{TableNames: []string{"t1"}},
	}
	stsClient := &fakeSTSClient{err: errors.New("denied")}
	_, err := filterDynamoDBTablesByProjectTag(context.Background(), ddb, stsClient, "us-east-1", "my-stack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sts GetCallerIdentity")
}

func TestFilterDynamoDBTablesByProjectTag_PerTableTagErrorSkips(t *testing.T) {
	t.Parallel()
	// ListTagsOfResource errors are log+skip — fail-closed.
	ddb := &fakeDynamoDBClient{
		tablesOut: &dynamodb.ListTablesOutput{TableNames: []string{"t1"}},
		tagsErr:   errors.New("denied"),
	}
	stsClient := &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")},
	}
	got, err := filterDynamoDBTablesByProjectTag(context.Background(), ddb, stsClient, "us-east-1", "my-stack")
	require.NoError(t, err)
	assert.Empty(t, got)
}
