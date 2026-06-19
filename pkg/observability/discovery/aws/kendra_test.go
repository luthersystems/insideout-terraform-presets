// AWS Kendra inspector tests (issue #760).
//
// Pins the #255 contract: empty list-indices / list-data-sources
// responses MUST marshal as JSON `[]`, never `null`. Also pins
// list-data-sources's required index_id surface and the metrics-routing
// arm. list-indices is the account-wide IndexId discovery action for the
// AWS/Kendra metrics namespace.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
	kendratypes "github.com/aws/aws-sdk-go-v2/service/kendra/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeKendraClient struct {
	listIndicesOut     *kendra.ListIndicesOutput
	listDataSourcesOut *kendra.ListDataSourcesOutput
	listDataSourcesIn  *kendra.ListDataSourcesInput
	err                error
}

func (f *fakeKendraClient) ListIndices(_ context.Context, _ *kendra.ListIndicesInput, _ ...func(*kendra.Options)) (*kendra.ListIndicesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.listIndicesOut == nil {
		return &kendra.ListIndicesOutput{}, nil
	}
	return f.listIndicesOut, nil
}

func (f *fakeKendraClient) ListDataSources(_ context.Context, in *kendra.ListDataSourcesInput, _ ...func(*kendra.Options)) (*kendra.ListDataSourcesOutput, error) {
	f.listDataSourcesIn = in
	if f.err != nil {
		return nil, f.err
	}
	if f.listDataSourcesOut == nil {
		return &kendra.ListDataSourcesOutput{}, nil
	}
	return f.listDataSourcesOut, nil
}

// TestListKendraIndices_EmptyResult — #255 contract: empty response is
// JSON `[]`, not `null`.
func TestListKendraIndices_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listKendraIndices(context.Background(), &fakeKendraClient{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty index list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListKendraIndices_ExplicitEmptySliceNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeKendraClient{listIndicesOut: &kendra.ListIndicesOutput{
		IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{}, // explicitly empty
	}}
	got, err := listKendraIndices(context.Background(), client)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListKendraIndices_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeKendraClient{
		listIndicesOut: &kendra.ListIndicesOutput{
			IndexConfigurationSummaryItems: []kendratypes.IndexConfigurationSummary{
				{Id: aws.String("idx-abc"), Name: aws.String("search-1")},
			},
		},
	}
	got, err := listKendraIndices(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "search-1", aws.ToString(got[0].Name))
	assert.Equal(t, "idx-abc", aws.ToString(got[0].Id))
}

func TestListKendraIndices_APIError(t *testing.T) {
	t.Parallel()
	_, err := listKendraIndices(context.Background(), &fakeKendraClient{err: errors.New("AccessDenied")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

// TestListKendraDataSources_EmptyResult — #255 contract: an empty
// data-source list must marshal as JSON `[]`, never `null`.
func TestListKendraDataSources_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listKendraDataSources(context.Background(), &fakeKendraClient{}, "idx-abc")
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty data-source list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListKendraDataSources_ExplicitEmptySliceNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeKendraClient{listDataSourcesOut: &kendra.ListDataSourcesOutput{
		SummaryItems: []kendratypes.DataSourceSummary{}, // explicitly empty
	}}
	got, err := listKendraDataSources(context.Background(), client, "idx-abc")
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListKendraDataSources_PassesIndexID(t *testing.T) {
	t.Parallel()
	client := &fakeKendraClient{}
	_, err := listKendraDataSources(context.Background(), client, "idx-xyz")
	require.NoError(t, err)
	require.NotNil(t, client.listDataSourcesIn)
	assert.Equal(t, "idx-xyz", aws.ToString(client.listDataSourcesIn.IndexId))
}

func TestListKendraDataSources_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeKendraClient{
		listDataSourcesOut: &kendra.ListDataSourcesOutput{
			SummaryItems: []kendratypes.DataSourceSummary{
				{Id: aws.String("ds-1"), Name: aws.String("s3-source")},
			},
		},
	}
	got, err := listKendraDataSources(context.Background(), client, "idx-abc")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "s3-source", aws.ToString(got[0].Name))
}

func TestListKendraDataSources_APIError(t *testing.T) {
	t.Parallel()
	_, err := listKendraDataSources(context.Background(), &fakeKendraClient{err: errors.New("AccessDenied")}, "idx-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

func TestKendraFilterIndexID_RequiresID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing key", `{"project":"demo"}`},
		{"empty value", `{"index_id":""}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := kendraFilterIndexID(tc.filters)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "index_id")
		})
	}
}

func TestKendraFilterIndexID_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := kendraFilterIndexID(`{not json}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid filters JSON")
}

func TestKendraFilterIndexID_Valid(t *testing.T) {
	t.Parallel()
	id, err := kendraFilterIndexID(`{"index_id":"idx-xyz"}`)
	require.NoError(t, err)
	assert.Equal(t, "idx-xyz", id)
}

// TestInspectKendra_GetMetricsRoutesToMetricsPackage — get-metrics
// short-circuits to the metrics-package sentinel.
func TestInspectKendra_GetMetricsRoutesToMetricsPackage(t *testing.T) {
	t.Parallel()
	_, err := inspectKendra(context.Background(), aws.Config{Region: "us-east-1"}, "get-metrics", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUseMetricsPackage)
	assert.Contains(t, err.Error(), "kendra")
}

func TestInspectKendra_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectKendra(context.Background(), aws.Config{Region: "us-east-1"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kendra")
	assert.Contains(t, err.Error(), "no-such-action")
}

// TestInspectKendra_ListDataSourcesRoutesToHandler — the list-data-sources
// arm must dispatch to the handler (here: the index_id-required guard),
// not fall through to unsupportedActionError. firstAction("kendra")
// returns list-indices, so TestInspectCoversAllAWSServices never
// exercises this arm.
func TestInspectKendra_ListDataSourcesRoutesToHandler(t *testing.T) {
	t.Parallel()
	_, err := inspectKendra(context.Background(), aws.Config{Region: "us-east-1"}, "list-data-sources", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index_id",
		"list-data-sources with no filters must hit the index_id-required guard, not unsupported-action")
	assert.NotContains(t, err.Error(), "no-such-action")
}
