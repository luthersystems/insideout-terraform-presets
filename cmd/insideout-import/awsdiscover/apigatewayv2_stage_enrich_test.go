package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestAPIGatewayV2StageEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_apigatewayv2_stage", newAPIGatewayV2StageEnricher().ResourceType())
}

func TestAPIGatewayV2StageEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := apigwV2StageEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Region: "us-east-1", NativeIDs: map[string]string{"api_id": "abc", "stage_name": "prod"}},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)

	_, err = enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{Region: "us-east-1", ImportID: "abc/prod"},
		EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestAPIGatewayV2StageEnricher_NilIdentityReturnsError(t *testing.T) {
	t.Parallel()
	enr := apigwV2StageEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{APIGatewayV2: &apigatewayv2.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestAPIGatewayV2StageEnricher_CannotResolveIdentity(t *testing.T) {
	t.Parallel()
	enr := apigwV2StageEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{}, // no NativeIDs, no ImportID
	}, EnrichClients{APIGatewayV2: &apigatewayv2.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve")
}

func TestAPIGatewayV2StageEnricher_TypedNotFoundReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	enr := apigwV2StageEnricher{fetch: func(context.Context, *apigatewayv2.Client, string, string, string) (*apigatewayv2.GetStageOutput, error) {
		return nil, &apigwv2types.NotFoundException{Message: aws.String("nope")}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Region:    "us-east-1",
			NativeIDs: map[string]string{"api_id": "abc", "stage_name": "prod"},
		},
	}, EnrichClients{APIGatewayV2: &apigatewayv2.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAPIGatewayV2StageEnricher_GenericErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDeniedException")
	enr := apigwV2StageEnricher{fetch: func(context.Context, *apigatewayv2.Client, string, string, string) (*apigatewayv2.GetStageOutput, error) {
		return nil, wantErr
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Region: "us-east-1", ImportID: "abc/prod"},
	}, EnrichClients{APIGatewayV2: &apigatewayv2.Client{}})
	require.ErrorIs(t, err, wantErr)
}

func TestAPIGatewayV2StageEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := apigwV2StageEnricher{fetch: func(_ context.Context, _ *apigatewayv2.Client, apiID, stage, region string) (*apigatewayv2.GetStageOutput, error) {
		assert.Equal(t, "abc", apiID)
		assert.Equal(t, "prod", stage)
		assert.Equal(t, "us-east-1", region)
		return &apigatewayv2.GetStageOutput{
			StageName:    aws.String("prod"),
			AutoDeploy:   aws.Bool(true),
			DeploymentId: aws.String("dep123"),
			Description:  aws.String("Production stage"),
			Tags:         map[string]string{"Project": "myproj", "Env": "prod"},
			StageVariables: map[string]string{
				"upstream": "https://api.example.com",
			},
			AccessLogSettings: &apigwv2types.AccessLogSettings{
				DestinationArn: aws.String("arn:aws:logs:us-east-1:111:log-group:/api-gw"),
				Format:         aws.String("$context.requestId"),
			},
			DefaultRouteSettings: &apigwv2types.RouteSettings{
				DetailedMetricsEnabled: aws.Bool(true),
				LoggingLevel:           apigwv2types.LoggingLevelInfo,
				ThrottlingBurstLimit:   aws.Int32(5000),
				ThrottlingRateLimit:    aws.Float64(10000),
			},
			RouteSettings: map[string]apigwv2types.RouteSettings{
				"GET /pets": {
					LoggingLevel: apigwv2types.LoggingLevelError,
				},
			},
		}, nil
	}}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Region:    "us-east-1",
			AccountID: "111122223333",
			NativeIDs: map[string]string{"api_id": "abc", "stage_name": "prod"},
			ImportID:  "abc/prod",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{APIGatewayV2: &apigatewayv2.Client{}}))
	require.NotEmpty(t, ir.Attrs)

	var got generated.AWSApigatewayv2Stage
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.APIID)
	assert.Equal(t, "abc", *got.APIID.Literal)
	require.NotNil(t, got.Name)
	assert.Equal(t, "prod", *got.Name.Literal)
	require.NotNil(t, got.AutoDeploy)
	assert.True(t, *got.AutoDeploy.Literal)
	require.NotNil(t, got.DeploymentID)
	assert.Equal(t, "dep123", *got.DeploymentID.Literal)
	require.Len(t, got.AccessLogSettings, 1)
	require.Len(t, got.DefaultRouteSettings, 1)
	require.Len(t, got.RouteSettings, 1)
	require.NotNil(t, got.RouteSettings[0].RouteKey)
	assert.Equal(t, "GET /pets", *got.RouteSettings[0].RouteKey.Literal)
	require.Contains(t, got.Tags, "Project")

	// ARN stamped on Identity.
	assert.Contains(t, ir.Identity.NativeIDs["arn"], "arn:aws:apigateway:us-east-1:111122223333::/apis/abc/stages/prod")
}

func TestAPIGatewayV2StageEnricher_ByIDDoesNotMutateIdentity(t *testing.T) {
	t.Parallel()
	enr := apigwV2StageEnricher{fetch: func(context.Context, *apigatewayv2.Client, string, string, string) (*apigatewayv2.GetStageOutput, error) {
		return &apigatewayv2.GetStageOutput{StageName: aws.String("prod")}, nil
	}}
	id := &imported.ResourceIdentity{
		Region:    "us-east-1",
		NativeIDs: map[string]string{"api_id": "abc", "stage_name": "prod"},
		ImportID:  "abc/prod",
	}
	originalLen := len(id.NativeIDs)
	raw, err := enr.EnrichByID(context.Background(), id, EnrichClients{APIGatewayV2: &apigatewayv2.Client{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	// EnrichByID must not stamp identity (no "arn" key added).
	assert.Equal(t, originalLen, len(id.NativeIDs))
}

func TestAPIGatewayV2StageIdentityParts_ImportIDFallback(t *testing.T) {
	t.Parallel()
	apiID, stage, err := apigwV2StageIdentityParts(&imported.ResourceIdentity{ImportID: "myapi/v1"})
	require.NoError(t, err)
	assert.Equal(t, "myapi", apiID)
	assert.Equal(t, "v1", stage)
}

func TestAPIGatewayV2StageIdentityParts_NativeIDsWin(t *testing.T) {
	t.Parallel()
	apiID, stage, err := apigwV2StageIdentityParts(&imported.ResourceIdentity{
		NativeIDs: map[string]string{"api_id": "from-native", "stage_name": "ns"},
		ImportID:  "should-not/use",
	})
	require.NoError(t, err)
	assert.Equal(t, "from-native", apiID)
	assert.Equal(t, "ns", stage)
}

func TestAPIGatewayV2StageARN_Format(t *testing.T) {
	t.Parallel()
	got := apigwV2StageARN("us-east-1", "111", "api", "prod")
	assert.Equal(t, "arn:aws:apigateway:us-east-1:111::/apis/api/stages/prod", got)
}
