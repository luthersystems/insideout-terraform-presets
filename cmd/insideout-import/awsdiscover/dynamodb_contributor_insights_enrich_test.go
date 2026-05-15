package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestDDBContributorInsightsEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_dynamodb_contributor_insights",
		newDDBContributorInsightsEnricher().ResourceType())
}

func TestDDBContributorInsightsEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := ddbContributorInsightsEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{ImportID: "my-table"},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestDDBContributorInsightsEnricher_NoTableNameReturnsError(t *testing.T) {
	t.Parallel()
	enr := ddbContributorInsightsEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{},
	}, EnrichClients{DynamoDB: &dynamodb.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive table name")
}

func TestDDBContributorInsightsEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := ddbContributorInsightsEnricher{fetch: func(_ context.Context, _ *dynamodb.Client, table string) (*dynamodb.DescribeContributorInsightsOutput, error) {
		assert.Equal(t, "my-table", table)
		return &dynamodb.DescribeContributorInsightsOutput{
			ContributorInsightsStatus: ddbtypes.ContributorInsightsStatusEnabled,
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		NativeIDs: map[string]string{"table_name": "my-table"},
		ImportID:  "my-table",
	}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	var got generated.AWSDynamodbContributorInsights
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.TableName)
	assert.Equal(t, "my-table", *got.TableName.Literal)
	require.NotNil(t, got.ID)
	assert.Equal(t, "my-table", *got.ID.Literal)
}

func TestDDBContributorInsightsEnricher_DisabledMapsToNotFound(t *testing.T) {
	t.Parallel()
	enr := ddbContributorInsightsEnricher{fetch: func(context.Context, *dynamodb.Client, string) (*dynamodb.DescribeContributorInsightsOutput, error) {
		return &dynamodb.DescribeContributorInsightsOutput{
			ContributorInsightsStatus: ddbtypes.ContributorInsightsStatusDisabled,
		}, nil
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{ImportID: "my-table"},
	}, EnrichClients{DynamoDB: &dynamodb.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDDBContributorInsightsEnricher_UnexpectedErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("ThrottlingException")
	enr := ddbContributorInsightsEnricher{fetch: func(context.Context, *dynamodb.Client, string) (*dynamodb.DescribeContributorInsightsOutput, error) {
		return nil, want
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{ImportID: "my-table"},
	}, EnrichClients{DynamoDB: &dynamodb.Client{}})
	require.ErrorIs(t, err, want)
}

func TestDDBContributorInsightsTableName_NativeIDsWin(t *testing.T) {
	t.Parallel()
	name, err := ddbContributorInsightsTableName(&imported.ResourceIdentity{
		NativeIDs: map[string]string{"table_name": "from-native"},
		ImportID:  "from-import",
	})
	require.NoError(t, err)
	assert.Equal(t, "from-native", name)
}

func TestDDBContributorInsightsTableName_ImportIDFallback(t *testing.T) {
	t.Parallel()
	name, err := ddbContributorInsightsTableName(&imported.ResourceIdentity{
		ImportID: "just-the-table",
	})
	require.NoError(t, err)
	assert.Equal(t, "just-the-table", name)
}
