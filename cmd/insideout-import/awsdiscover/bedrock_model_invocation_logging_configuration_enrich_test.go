package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// newTestBedrockModelInvocationLoggingConfigurationEnricher injects a
// fake GetModelInvocationLoggingConfiguration hook. The SDK call takes
// no input so the fake's signature is the simplest of any AWS-side
// enricher — just (ctx, *Client) → (*output, error).
func newTestBedrockModelInvocationLoggingConfigurationEnricher(
	get func(ctx context.Context, c *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error),
) *bedrockModelInvocationLoggingConfigurationEnricher {
	return &bedrockModelInvocationLoggingConfigurationEnricher{fetch: get}
}

func decodeBedrockMILCAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSBedrockModelInvocationLoggingConfiguration {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_bedrock_model_invocation_logging_configuration", ir.Attrs)
	require.NoError(t, err)
	g, ok := decoded.(*generated.AWSBedrockModelInvocationLoggingConfiguration)
	require.True(t, ok, "decoded type must be *AWSBedrockModelInvocationLoggingConfiguration, got %T", decoded)
	return g
}

func decodeBedrockMILCRaw(t *testing.T, raw json.RawMessage) *generated.AWSBedrockModelInvocationLoggingConfiguration {
	t.Helper()
	require.NotEmpty(t, raw, "EnrichByID must return a non-empty payload")
	decoded, err := generated.UnmarshalAttrs("aws_bedrock_model_invocation_logging_configuration", raw)
	require.NoError(t, err)
	g, ok := decoded.(*generated.AWSBedrockModelInvocationLoggingConfiguration)
	require.True(t, ok, "decoded type must be *AWSBedrockModelInvocationLoggingConfiguration, got %T", decoded)
	return g
}

func TestBedrockMILCEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newBedrockModelInvocationLoggingConfigurationEnricher()
	assert.Equal(t, "aws_bedrock_model_invocation_logging_configuration", enr.ResourceType())
}

func TestBedrockMILCEnricher_ImplementsByIDEnricher(t *testing.T) {
	t.Parallel()
	var _ AttributeEnricher = newBedrockModelInvocationLoggingConfigurationEnricher()
	enr := newBedrockModelInvocationLoggingConfigurationEnricher()
	var _ ByIDEnricher = enr
}

func TestBedrockMILCEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := bedrockModelInvocationLoggingConfigurationEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_bedrock_model_invocation_logging_configuration",
			Region:   "us-east-1",
			ImportID: "us-east-1",
		},
	}, EnrichClients{Bedrock: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestBedrockMILCEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := bedrockModelInvocationLoggingConfigurationEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_bedrock_model_invocation_logging_configuration",
		ImportID: "us-east-1",
	}, EnrichClients{Bedrock: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestBedrockMILCEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := newBedrockModelInvocationLoggingConfigurationEnricher()
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{Bedrock: &bedrock.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestBedrockMILCEnricher_NotFoundMapsToErrNotFound(t *testing.T) {
	t.Parallel()
	t.Run("typed_NotFound", func(t *testing.T) {
		t.Parallel()
		enr := newTestBedrockModelInvocationLoggingConfigurationEnricher(
			func(context.Context, *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error) {
				return nil, &bedrocktypes.ResourceNotFoundException{Message: aws.String("not found")}
			},
		)
		err := enr.Enrich(context.Background(), &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:   "aws_bedrock_model_invocation_logging_configuration",
				Region: "us-east-1",
			},
		}, EnrichClients{Bedrock: &bedrock.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
	t.Run("nil_LoggingConfig", func(t *testing.T) {
		t.Parallel()
		// 200 OK with nil payload is the "unconfigured" shape; treat it
		// identically to typed not-found.
		enr := newTestBedrockModelInvocationLoggingConfigurationEnricher(
			func(context.Context, *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error) {
				return &bedrock.GetModelInvocationLoggingConfigurationOutput{}, nil
			},
		)
		err := enr.Enrich(context.Background(), &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:   "aws_bedrock_model_invocation_logging_configuration",
				Region: "us-east-1",
			},
		}, EnrichClients{Bedrock: &bedrock.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestBedrockMILCEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDenied")
	enr := newTestBedrockModelInvocationLoggingConfigurationEnricher(
		func(context.Context, *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error) {
			return nil, wantErr
		},
	)
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:   "aws_bedrock_model_invocation_logging_configuration",
			Region: "us-east-1",
		},
	}, EnrichClients{Bedrock: &bedrock.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestBedrockMILCEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	const (
		region       = "us-east-1"
		logGroupName = "/aws/bedrock/invocations"
		roleARN      = "arn:aws:iam::012345678901:role/bedrock-logging"
		s3Bucket     = "my-bedrock-logs"
		s3Prefix     = "model-invocations/"
		ldsBucket    = "my-bedrock-large-data"
	)
	out := &bedrock.GetModelInvocationLoggingConfigurationOutput{
		LoggingConfig: &bedrocktypes.LoggingConfig{
			TextDataDeliveryEnabled:      aws.Bool(true),
			ImageDataDeliveryEnabled:     aws.Bool(false),
			EmbeddingDataDeliveryEnabled: aws.Bool(true),
			CloudWatchConfig: &bedrocktypes.CloudWatchConfig{
				LogGroupName: aws.String(logGroupName),
				RoleArn:      aws.String(roleARN),
				LargeDataDeliveryS3Config: &bedrocktypes.S3Config{
					BucketName: aws.String(ldsBucket),
					KeyPrefix:  aws.String("large/"),
				},
			},
			S3Config: &bedrocktypes.S3Config{
				BucketName: aws.String(s3Bucket),
				KeyPrefix:  aws.String(s3Prefix),
			},
		},
	}
	enr := newTestBedrockModelInvocationLoggingConfigurationEnricher(
		func(context.Context, *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error) {
			return out, nil
		},
	)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_bedrock_model_invocation_logging_configuration",
			Region:   region,
			ImportID: region,
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{Bedrock: &bedrock.Client{}}))

	g := decodeBedrockMILCAttrs(t, ir)
	require.NotNil(t, g.ID)
	assert.Equal(t, region, *g.ID.Literal, "id is the region per TF provider convention")

	require.NotNil(t, g.LoggingConfig)
	require.NotNil(t, g.LoggingConfig.TextDataDeliveryEnabled)
	assert.Equal(t, true, *g.LoggingConfig.TextDataDeliveryEnabled.Literal)
	require.NotNil(t, g.LoggingConfig.ImageDataDeliveryEnabled)
	assert.Equal(t, false, *g.LoggingConfig.ImageDataDeliveryEnabled.Literal)
	require.NotNil(t, g.LoggingConfig.EmbeddingDataDeliveryEnabled)
	assert.Equal(t, true, *g.LoggingConfig.EmbeddingDataDeliveryEnabled.Literal)

	require.NotNil(t, g.LoggingConfig.CloudwatchConfig)
	require.NotNil(t, g.LoggingConfig.CloudwatchConfig.LogGroupName)
	assert.Equal(t, logGroupName, *g.LoggingConfig.CloudwatchConfig.LogGroupName.Literal)
	require.NotNil(t, g.LoggingConfig.CloudwatchConfig.RoleARN)
	assert.Equal(t, roleARN, *g.LoggingConfig.CloudwatchConfig.RoleARN.Literal)
	require.NotNil(t, g.LoggingConfig.CloudwatchConfig.LargeDataDeliveryS3Config)
	require.NotNil(t, g.LoggingConfig.CloudwatchConfig.LargeDataDeliveryS3Config.BucketName)
	assert.Equal(t, ldsBucket, *g.LoggingConfig.CloudwatchConfig.LargeDataDeliveryS3Config.BucketName.Literal)
	require.NotNil(t, g.LoggingConfig.CloudwatchConfig.LargeDataDeliveryS3Config.KeyPrefix)
	assert.Equal(t, "large/", *g.LoggingConfig.CloudwatchConfig.LargeDataDeliveryS3Config.KeyPrefix.Literal)

	require.NotNil(t, g.LoggingConfig.S3Config)
	require.NotNil(t, g.LoggingConfig.S3Config.BucketName)
	assert.Equal(t, s3Bucket, *g.LoggingConfig.S3Config.BucketName.Literal)
	require.NotNil(t, g.LoggingConfig.S3Config.KeyPrefix)
	assert.Equal(t, s3Prefix, *g.LoggingConfig.S3Config.KeyPrefix.Literal)
}

func TestBedrockMILCEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	const region = "us-west-2"
	out := &bedrock.GetModelInvocationLoggingConfigurationOutput{
		LoggingConfig: &bedrocktypes.LoggingConfig{
			TextDataDeliveryEnabled: aws.Bool(true),
			S3Config: &bedrocktypes.S3Config{
				BucketName: aws.String("test-bucket"),
			},
		},
	}
	enr := newTestBedrockModelInvocationLoggingConfigurationEnricher(
		func(context.Context, *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error) {
			return out, nil
		},
	)

	// Enrich path.
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:   "aws_bedrock_model_invocation_logging_configuration",
			Region: region,
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{Bedrock: &bedrock.Client{}}))
	gFromEnrich := decodeBedrockMILCAttrs(t, ir)

	// EnrichByID path — Identity.ImportID fallback when Region empty.
	identity := &imported.ResourceIdentity{
		Type:     "aws_bedrock_model_invocation_logging_configuration",
		ImportID: region,
	}
	raw, err := enr.EnrichByID(context.Background(), identity, EnrichClients{Bedrock: &bedrock.Client{}})
	require.NoError(t, err)
	gFromByID := decodeBedrockMILCRaw(t, raw)

	assert.Equal(t, gFromEnrich, gFromByID,
		"Enrich and EnrichByID must produce identical typed payloads")
}
