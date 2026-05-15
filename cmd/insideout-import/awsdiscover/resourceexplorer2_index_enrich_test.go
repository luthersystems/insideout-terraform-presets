package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	re2types "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// newTestResourceExplorer2IndexEnricher builds a
// resourceExplorer2IndexEnricher with a fake fetch closure wired in.
// Mirrors newTestCloudWatchLogGroupEnricher.
func newTestResourceExplorer2IndexEnricher(
	fetch func(ctx context.Context, c *resourceexplorer2.Client, region string) (*resourceexplorer2.GetIndexOutput, error),
) resourceExplorer2IndexEnricher {
	return resourceExplorer2IndexEnricher{fetch: fetch}
}

func decodeResourceExplorer2IndexAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSResourceexplorer2Index {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_resourceexplorer2_index", ir.Attrs)
	require.NoError(t, err)
	idx, ok := decoded.(*generated.AWSResourceexplorer2Index)
	require.True(t, ok, "decoded type must be *AWSResourceexplorer2Index, got %T", decoded)
	return idx
}

func TestResourceExplorer2IndexEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newResourceExplorer2IndexEnricher()
	assert.Equal(t, "aws_resourceexplorer2_index", enr.ResourceType())
}

// Compile-time pin: resourceExplorer2IndexEnricher must satisfy both
// AttributeEnricher and ByIDEnricher.
var (
	_ AttributeEnricher = (*resourceExplorer2IndexEnricher)(nil)
	_ ByIDEnricher      = (*resourceExplorer2IndexEnricher)(nil)
)

func TestResourceExplorer2IndexEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := resourceExplorer2IndexEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_resourceexplorer2_index", Region: "us-east-1"},
	}, EnrichClients{ResourceExplorer2: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestResourceExplorer2IndexEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := resourceExplorer2IndexEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_resourceexplorer2_index", Region: "us-east-1",
	}, EnrichClients{ResourceExplorer2: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestResourceExplorer2IndexEnricher_EnrichByID_NilIdentityReturnsError(t *testing.T) {
	t.Parallel()
	enr := resourceExplorer2IndexEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestResourceExplorer2IndexEnricher_GetIndexErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDeniedException")
	enr := newTestResourceExplorer2IndexEnricher(
		func(context.Context, *resourceexplorer2.Client, string) (*resourceexplorer2.GetIndexOutput, error) {
			return nil, wantErr
		})
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_resourceexplorer2_index", Region: "us-east-1"},
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestResourceExplorer2IndexEnricher_TypedNotFoundReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	enr := newTestResourceExplorer2IndexEnricher(
		func(context.Context, *resourceexplorer2.Client, string) (*resourceexplorer2.GetIndexOutput, error) {
			return nil, &re2types.ResourceNotFoundException{Message: aws.String("no index")}
		})

	// Enrich path.
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_resourceexplorer2_index", Region: "us-east-1"},
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)

	// EnrichByID path.
	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_resourceexplorer2_index", Region: "us-east-1",
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Empty(t, raw)
}

func TestResourceExplorer2IndexEnricher_EmptyResponseTreatedAsNotFound(t *testing.T) {
	t.Parallel()
	// GetIndex returns an empty response (Arn==nil) when no index is
	// configured in the region. The enricher surfaces that as
	// ErrNotFound so by-ID callers can distinguish "absent" from a real
	// API failure.
	enr := newTestResourceExplorer2IndexEnricher(
		func(context.Context, *resourceexplorer2.Client, string) (*resourceexplorer2.GetIndexOutput, error) {
			return &resourceexplorer2.GetIndexOutput{}, nil
		})
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_resourceexplorer2_index", Region: "us-east-1"},
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestResourceExplorer2IndexEnricher_BasicMapping(t *testing.T) {
	t.Parallel()
	wantARN := "arn:aws:resource-explorer-2:us-east-1:012345678901:index/abc123-uuid"
	enr := newTestResourceExplorer2IndexEnricher(
		func(_ context.Context, _ *resourceexplorer2.Client, region string) (*resourceexplorer2.GetIndexOutput, error) {
			assert.Equal(t, "us-east-1", region, "fetch must receive the Identity.Region")
			return &resourceexplorer2.GetIndexOutput{
				Arn:  aws.String(wantARN),
				Type: re2types.IndexTypeLocal,
				Tags: map[string]string{
					"Env":  "prod",
					"Team": "platform",
				},
			}, nil
		})

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:    "aws_resourceexplorer2_index",
			Address: "aws_resourceexplorer2_index.index_us_east_1",
			Region:  "us-east-1",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}}))

	// ARN stamped onto NativeIDs.
	assert.Equal(t, wantARN, ir.Identity.NativeIDs["arn"])

	out := decodeResourceExplorer2IndexAttrs(t, ir)

	require.NotNil(t, out.ARN)
	assert.Equal(t, wantARN, *out.ARN.Literal)
	require.NotNil(t, out.ID)
	assert.Equal(t, wantARN, *out.ID.Literal, "id must mirror ARN per TF state semantics")
	require.NotNil(t, out.Type_)
	assert.Equal(t, string(re2types.IndexTypeLocal), *out.Type_.Literal)

	require.NotNil(t, out.Tags)
	require.NotNil(t, out.Tags["Env"])
	assert.Equal(t, "prod", *out.Tags["Env"].Literal)
	require.NotNil(t, out.Tags["Team"])
	assert.Equal(t, "platform", *out.Tags["Team"].Literal)

	// Decision-#5 skipped fields must remain unset.
	assert.Empty(t, out.TagsAll, "tags_all is a computed mirror — enricher must not populate")
	assert.Nil(t, out.Timeouts, "timeouts is TF-input-only — must not be set from SDK")
}

func TestResourceExplorer2IndexEnricher_OmitsTagsWhenAbsent(t *testing.T) {
	t.Parallel()
	enr := newTestResourceExplorer2IndexEnricher(
		func(context.Context, *resourceexplorer2.Client, string) (*resourceexplorer2.GetIndexOutput, error) {
			return &resourceexplorer2.GetIndexOutput{
				Arn:  aws.String("arn:aws:resource-explorer-2:us-east-1:012345678901:index/uuid"),
				Type: re2types.IndexTypeAggregator,
			}, nil
		})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_resourceexplorer2_index", Region: "us-east-1"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}}))

	out := decodeResourceExplorer2IndexAttrs(t, ir)
	assert.Empty(t, out.Tags, "tags must be absent when SDK returns no Tags map")
	require.NotNil(t, out.Type_)
	assert.Equal(t, string(re2types.IndexTypeAggregator), *out.Type_.Literal)
}

func TestResourceExplorer2IndexEnricher_EnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	wantARN := "arn:aws:resource-explorer-2:us-west-2:012345678901:index/byid-uuid"
	enr := newTestResourceExplorer2IndexEnricher(
		func(_ context.Context, _ *resourceexplorer2.Client, region string) (*resourceexplorer2.GetIndexOutput, error) {
			assert.Equal(t, "us-west-2", region)
			return &resourceexplorer2.GetIndexOutput{
				Arn:  aws.String(wantARN),
				Type: re2types.IndexTypeLocal,
			}, nil
		})

	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:   "aws_resourceexplorer2_index",
		Region: "us-west-2",
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	decoded, err := generated.UnmarshalAttrs("aws_resourceexplorer2_index", raw)
	require.NoError(t, err)
	out, ok := decoded.(*generated.AWSResourceexplorer2Index)
	require.True(t, ok)
	require.NotNil(t, out.ARN)
	assert.Equal(t, wantARN, *out.ARN.Literal)
}

// Sanity pin: Enrich and EnrichByID must produce byte-identical JSON
// for the same fixture. Catches a future refactor that splits the
// mapping into divergent paths.
func TestResourceExplorer2IndexEnricher_EnrichAndEnrichByIDProduceSameJSON(t *testing.T) {
	t.Parallel()
	wantARN := "arn:aws:resource-explorer-2:us-east-1:012345678901:index/same"
	fetch := func(context.Context, *resourceexplorer2.Client, string) (*resourceexplorer2.GetIndexOutput, error) {
		return &resourceexplorer2.GetIndexOutput{
			Arn:  aws.String(wantARN),
			Type: re2types.IndexTypeAggregator,
			Tags: map[string]string{"K": "V"},
		}, nil
	}
	enr := newTestResourceExplorer2IndexEnricher(fetch)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_resourceexplorer2_index", Region: "us-east-1"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}}))

	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_resourceexplorer2_index", Region: "us-east-1",
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.NoError(t, err)

	// Compare decoded structs rather than raw bytes (Go's map iteration
	// order can shuffle the tag JSON without changing semantics).
	var fromEnrich, fromByID json.RawMessage = ir.Attrs, raw
	dEnrich, err := generated.UnmarshalAttrs("aws_resourceexplorer2_index", fromEnrich)
	require.NoError(t, err)
	dByID, err := generated.UnmarshalAttrs("aws_resourceexplorer2_index", fromByID)
	require.NoError(t, err)
	assert.Equal(t, dEnrich, dByID)
}
