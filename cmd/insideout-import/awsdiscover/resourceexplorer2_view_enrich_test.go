package awsdiscover

import (
	"context"
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

// newTestResourceExplorer2ViewEnricher builds a
// resourceExplorer2ViewEnricher with a fake fetch closure wired in.
func newTestResourceExplorer2ViewEnricher(
	fetch func(ctx context.Context, c *resourceexplorer2.Client, region, viewARN string) (*resourceexplorer2.GetViewOutput, error),
) resourceExplorer2ViewEnricher {
	return resourceExplorer2ViewEnricher{fetch: fetch}
}

func decodeResourceExplorer2ViewAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSResourceexplorer2View {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_resourceexplorer2_view", ir.Attrs)
	require.NoError(t, err)
	v, ok := decoded.(*generated.AWSResourceexplorer2View)
	require.True(t, ok, "decoded type must be *AWSResourceexplorer2View, got %T", decoded)
	return v
}

func TestResourceExplorer2ViewEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newResourceExplorer2ViewEnricher()
	assert.Equal(t, "aws_resourceexplorer2_view", enr.ResourceType())
}

// Compile-time pin: resourceExplorer2ViewEnricher must satisfy both
// AttributeEnricher and ByIDEnricher.
var (
	_ AttributeEnricher = (*resourceExplorer2ViewEnricher)(nil)
	_ ByIDEnricher      = (*resourceExplorer2ViewEnricher)(nil)
)

func TestResourceExplorer2ViewEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := resourceExplorer2ViewEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_resourceexplorer2_view",
			Region:   "us-east-1",
			ImportID: "arn:aws:resource-explorer-2:us-east-1:012345678901:view/v/uuid",
		},
	}, EnrichClients{ResourceExplorer2: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestResourceExplorer2ViewEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := resourceExplorer2ViewEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_resourceexplorer2_view",
		Region:   "us-east-1",
		ImportID: "arn:aws:resource-explorer-2:us-east-1:012345678901:view/v/uuid",
	}, EnrichClients{ResourceExplorer2: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestResourceExplorer2ViewEnricher_EnrichByID_NilIdentityReturnsError(t *testing.T) {
	t.Parallel()
	enr := resourceExplorer2ViewEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestResourceExplorer2ViewEnricher_ARNDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		id    imported.ResourceIdentity
		want  string
	}{
		{
			label: "ImportID wins",
			id: imported.ResourceIdentity{
				ImportID:  "arn:aws:resource-explorer-2:us-east-1:012345678901:view/from-import/uuid",
				NativeIDs: map[string]string{"arn": "arn:aws:resource-explorer-2:us-east-1:012345678901:view/from-native/uuid"},
			},
			want: "arn:aws:resource-explorer-2:us-east-1:012345678901:view/from-import/uuid",
		},
		{
			label: "NativeIDs[arn] is fallback",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"arn": "arn:aws:resource-explorer-2:us-east-1:012345678901:view/from-native/uuid"},
			},
			want: "arn:aws:resource-explorer-2:us-east-1:012345678901:view/from-native/uuid",
		},
		{
			label: "empty everywhere -> empty",
			id:    imported.ResourceIdentity{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			assert.Equal(t, tc.want, resourceExplorer2ViewARNForEnrich(&tc.id))
		})
	}
}

func TestResourceExplorer2ViewEnricher_NoARNReturnsError(t *testing.T) {
	t.Parallel()
	enr := newResourceExplorer2ViewEnricher()
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_resourceexplorer2_view", Region: "us-east-1"},
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive view ARN")
}

func TestResourceExplorer2ViewEnricher_GetViewErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDeniedException")
	enr := newTestResourceExplorer2ViewEnricher(
		func(context.Context, *resourceexplorer2.Client, string, string) (*resourceexplorer2.GetViewOutput, error) {
			return nil, wantErr
		})
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_resourceexplorer2_view",
			Region:   "us-east-1",
			ImportID: "arn:aws:resource-explorer-2:us-east-1:012345678901:view/v/uuid",
		},
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestResourceExplorer2ViewEnricher_TypedNotFoundReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	enr := newTestResourceExplorer2ViewEnricher(
		func(context.Context, *resourceexplorer2.Client, string, string) (*resourceexplorer2.GetViewOutput, error) {
			return nil, &re2types.ResourceNotFoundException{Message: aws.String("missing view")}
		})

	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_resourceexplorer2_view",
			Region:   "us-east-1",
			ImportID: "arn:aws:resource-explorer-2:us-east-1:012345678901:view/missing/uuid",
		},
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestResourceExplorer2ViewEnricher_NilViewBodyTreatedAsNotFound(t *testing.T) {
	t.Parallel()
	enr := newTestResourceExplorer2ViewEnricher(
		func(context.Context, *resourceexplorer2.Client, string, string) (*resourceexplorer2.GetViewOutput, error) {
			return &resourceexplorer2.GetViewOutput{}, nil // out.View == nil
		})
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_resourceexplorer2_view",
			Region:   "us-east-1",
			ImportID: "arn:aws:resource-explorer-2:us-east-1:012345678901:view/v/uuid",
		},
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestResourceExplorer2ViewEnricher_BasicMapping(t *testing.T) {
	t.Parallel()
	wantARN := "arn:aws:resource-explorer-2:us-east-1:012345678901:view/inventory/uuid"
	enr := newTestResourceExplorer2ViewEnricher(
		func(_ context.Context, _ *resourceexplorer2.Client, region, viewARN string) (*resourceexplorer2.GetViewOutput, error) {
			assert.Equal(t, "us-east-1", region, "fetch must receive the Identity.Region")
			assert.Equal(t, wantARN, viewARN, "fetch must receive the resolved ViewArn")
			return &resourceexplorer2.GetViewOutput{
				View: &re2types.View{
					ViewArn: aws.String(wantARN),
					Filters: &re2types.SearchFilter{FilterString: aws.String("service:ec2")},
					IncludedProperties: []re2types.IncludedProperty{
						{Name: aws.String("tags")},
					},
				},
				Tags: map[string]string{
					"Env":  "prod",
					"Team": "platform",
				},
			}, nil
		})

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_resourceexplorer2_view",
			Address:  "aws_resourceexplorer2_view.inventory",
			Region:   "us-east-1",
			ImportID: wantARN,
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}}))

	// ARN stamped onto NativeIDs.
	assert.Equal(t, wantARN, ir.Identity.NativeIDs["arn"])

	out := decodeResourceExplorer2ViewAttrs(t, ir)

	require.NotNil(t, out.ARN)
	assert.Equal(t, wantARN, *out.ARN.Literal)
	require.NotNil(t, out.ID)
	assert.Equal(t, wantARN, *out.ID.Literal, "id must mirror ARN per TF state semantics")
	require.NotNil(t, out.Name)
	assert.Equal(t, "inventory", *out.Name.Literal, "name must be parsed from the ARN path")

	require.Len(t, out.Filters, 1)
	require.NotNil(t, out.Filters[0].FilterString)
	assert.Equal(t, "service:ec2", *out.Filters[0].FilterString.Literal)

	require.Len(t, out.IncludedProperty, 1)
	require.NotNil(t, out.IncludedProperty[0].Name)
	assert.Equal(t, "tags", *out.IncludedProperty[0].Name.Literal)

	require.NotNil(t, out.Tags)
	require.NotNil(t, out.Tags["Env"])
	assert.Equal(t, "prod", *out.Tags["Env"].Literal)

	// Decision-#5 skipped fields must remain unset.
	assert.Empty(t, out.TagsAll, "tags_all is a computed mirror — enricher must not populate")
	assert.Nil(t, out.DefaultView, "default_view is not exposed by GetView — enricher must not guess")
}

func TestResourceExplorer2ViewEnricher_OmitsOptionalFieldsWhenUnset(t *testing.T) {
	t.Parallel()
	wantARN := "arn:aws:resource-explorer-2:us-east-1:012345678901:view/minimal/uuid"
	enr := newTestResourceExplorer2ViewEnricher(
		func(context.Context, *resourceexplorer2.Client, string, string) (*resourceexplorer2.GetViewOutput, error) {
			return &resourceexplorer2.GetViewOutput{
				View: &re2types.View{ViewArn: aws.String(wantARN)},
			}, nil
		})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_resourceexplorer2_view",
			Region:   "us-east-1",
			ImportID: wantARN,
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}}))

	out := decodeResourceExplorer2ViewAttrs(t, ir)
	assert.Empty(t, out.Filters, "filters must be absent when SDK returns nil")
	assert.Empty(t, out.IncludedProperty, "included_property must be absent when SDK returns empty slice")
	assert.Empty(t, out.Tags, "tags must be absent when GetView returns no Tags")
}

func TestResourceExplorer2ViewEnricher_EnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	wantARN := "arn:aws:resource-explorer-2:us-west-2:012345678901:view/byid/uuid"
	enr := newTestResourceExplorer2ViewEnricher(
		func(_ context.Context, _ *resourceexplorer2.Client, region, viewARN string) (*resourceexplorer2.GetViewOutput, error) {
			assert.Equal(t, "us-west-2", region)
			assert.Equal(t, wantARN, viewARN)
			return &resourceexplorer2.GetViewOutput{
				View: &re2types.View{ViewArn: aws.String(wantARN)},
			}, nil
		})

	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_resourceexplorer2_view",
		Region:   "us-west-2",
		ImportID: wantARN,
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	decoded, err := generated.UnmarshalAttrs("aws_resourceexplorer2_view", raw)
	require.NoError(t, err)
	out, ok := decoded.(*generated.AWSResourceexplorer2View)
	require.True(t, ok)
	require.NotNil(t, out.ARN)
	assert.Equal(t, wantARN, *out.ARN.Literal)
	require.NotNil(t, out.Name)
	assert.Equal(t, "byid", *out.Name.Literal)
}

// Sanity pin: Enrich and EnrichByID must produce semantically identical
// JSON for the same fixture.
func TestResourceExplorer2ViewEnricher_EnrichAndEnrichByIDProduceSameJSON(t *testing.T) {
	t.Parallel()
	wantARN := "arn:aws:resource-explorer-2:us-east-1:012345678901:view/same/uuid"
	fetch := func(context.Context, *resourceexplorer2.Client, string, string) (*resourceexplorer2.GetViewOutput, error) {
		return &resourceexplorer2.GetViewOutput{
			View: &re2types.View{
				ViewArn: aws.String(wantARN),
				Filters: &re2types.SearchFilter{FilterString: aws.String("region:us-east-1")},
			},
			Tags: map[string]string{"K": "V"},
		}, nil
	}
	enr := newTestResourceExplorer2ViewEnricher(fetch)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_resourceexplorer2_view",
			Region:   "us-east-1",
			ImportID: wantARN,
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}}))

	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_resourceexplorer2_view", Region: "us-east-1", ImportID: wantARN,
	}, EnrichClients{ResourceExplorer2: &resourceexplorer2.Client{}})
	require.NoError(t, err)

	dEnrich, err := generated.UnmarshalAttrs("aws_resourceexplorer2_view", ir.Attrs)
	require.NoError(t, err)
	dByID, err := generated.UnmarshalAttrs("aws_resourceexplorer2_view", raw)
	require.NoError(t, err)
	assert.Equal(t, dEnrich, dByID)
}
