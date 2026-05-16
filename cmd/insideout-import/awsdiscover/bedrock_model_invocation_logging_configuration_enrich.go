package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// bedrockModelInvocationLoggingConfigurationEnricher implements both
// AttributeEnricher and ByIDEnricher for
// aws_bedrock_model_invocation_logging_configuration (#482 Bucket-C
// push). The TF resource is a per-region account singleton — its import
// id IS the region, and there is no per-resource identifier. The
// GetModelInvocationLoggingConfiguration SDK call takes no input
// arguments; the region is implicit on the SDK client.
//
// Hand-rolled discoverer rationale (mirrored from
// bedrock_model_invocation_logging_configuration.go): Cloud Control
// returns TypeNotFoundException for the CFN
// AWS::Bedrock::ModelInvocationLoggingConfiguration on at least one
// real account profile, so the unified path can't enrich this type.
// Native bedrock SDK end-to-end is the only working path.
//
// Per decision #5, Computed-only TF fields are populated when they
// exist on the API response — `id` is the only such field on this
// resource (the TF provider stores the region as the id).
//
// Sensitive fields: none on this resource — the logging configuration
// references log groups and S3 buckets by name, not data values.
// Decision #36 redaction stays downstream.
type bedrockModelInvocationLoggingConfigurationEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// GetModelInvocationLoggingConfiguration call against the
	// bedrock.Client in EnrichClients. The SDK call takes no input
	// arguments, so the fetch hook's signature is the simplest of any
	// AWS-side enricher.
	fetch func(ctx context.Context, c *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error)
}

// newBedrockModelInvocationLoggingConfigurationEnricher returns the
// production-wired enricher. AWSDiscoverer's byTypeEnricher map
// registers this under "aws_bedrock_model_invocation_logging_configuration".
func newBedrockModelInvocationLoggingConfigurationEnricher() *bedrockModelInvocationLoggingConfigurationEnricher {
	return &bedrockModelInvocationLoggingConfigurationEnricher{fetch: defaultBedrockModelInvocationLoggingConfigurationFetch}
}

func (bedrockModelInvocationLoggingConfigurationEnricher) ResourceType() string {
	return bedrockModelInvocationLoggingConfigurationTFType
}

// Enrich populates ir.Attrs with a typed
// AWSBedrockModelInvocationLoggingConfiguration payload. Because the
// resource is a per-region account singleton, no identifier derivation
// is required — the SDK client's bound region selects the row, and
// either it exists (200 OK with non-nil LoggingConfig) or it doesn't
// (200 OK with nil LoggingConfig, or typed
// ResourceNotFoundException).
//
// Returns ErrEnrichClientUnavailable if EnrichClients.Bedrock is nil;
// ErrNotFound when the singleton is unconfigured for this region.
func (e bedrockModelInvocationLoggingConfigurationEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.Bedrock == nil {
		return ErrEnrichClientUnavailable
	}
	region := ir.Identity.Region
	out, err := e.fetch(ctx, c.Bedrock)
	if err != nil {
		var notFound *bedrocktypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return fmt.Errorf("bedrock_model_invocation_logging_configuration %q: %w", region, ErrNotFound)
		}
		return fmt.Errorf("bedrock_model_invocation_logging_configuration: get (region=%s): %w", region, err)
	}
	if out == nil || out.LoggingConfig == nil {
		return fmt.Errorf("bedrock_model_invocation_logging_configuration %q: %w", region, ErrNotFound)
	}

	typed := mapBedrockModelInvocationLoggingConfiguration(out, region)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("bedrock_model_invocation_logging_configuration: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed payload for the singleton in the
// region carried on identity.Region (or, when callers populate it
// instead, identity.ImportID — the TF import id IS the region string).
// Shares the SDK call + mapping with Enrich via the private
// mapBedrockModelInvocationLoggingConfiguration helper.
func (e bedrockModelInvocationLoggingConfigurationEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("bedrock_model_invocation_logging_configuration: nil identity")
	}
	if c.Bedrock == nil {
		return nil, ErrEnrichClientUnavailable
	}
	region := identity.Region
	if region == "" {
		region = identity.ImportID
	}
	out, err := e.fetch(ctx, c.Bedrock)
	if err != nil {
		var notFound *bedrocktypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("bedrock_model_invocation_logging_configuration %q: %w", region, ErrNotFound)
		}
		return nil, fmt.Errorf("bedrock_model_invocation_logging_configuration: get (region=%s): %w", region, err)
	}
	if out == nil || out.LoggingConfig == nil {
		return nil, fmt.Errorf("bedrock_model_invocation_logging_configuration %q: %w", region, ErrNotFound)
	}
	typed := mapBedrockModelInvocationLoggingConfiguration(out, region)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("bedrock_model_invocation_logging_configuration: marshal Attrs: %w", err)
	}
	return raw, nil
}

// defaultBedrockModelInvocationLoggingConfigurationFetch is the
// production fetch path: a single GetModelInvocationLoggingConfiguration
// call. The SDK call takes no input arguments — the region is implicit
// on the bedrock.Client.
func defaultBedrockModelInvocationLoggingConfigurationFetch(ctx context.Context, c *bedrock.Client) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error) {
	return c.GetModelInvocationLoggingConfiguration(ctx, &bedrock.GetModelInvocationLoggingConfigurationInput{})
}

// mapBedrockModelInvocationLoggingConfiguration is the pure-mapping
// helper shared by Enrich and EnrichByID. The Layer 1 typed surface is
// small — just the `logging_config` block with three boolean data-class
// toggles and two destination sub-blocks (CloudWatch + S3) — so the
// mapping is straightforward field-by-field.
//
// The `id` Layer 1 field is populated with the region per the TF
// provider's id-is-region convention.
//
// Decision-#34 cleanliness: every field is emitted only when present on
// the API response, so the resulting HCL does not contain "field =
// null" noise.
func mapBedrockModelInvocationLoggingConfiguration(out *bedrock.GetModelInvocationLoggingConfigurationOutput, region string) *generated.AWSBedrockModelInvocationLoggingConfiguration {
	typed := &generated.AWSBedrockModelInvocationLoggingConfiguration{}
	if region != "" {
		typed.ID = generated.LiteralOf(region)
	}
	if out == nil || out.LoggingConfig == nil {
		return typed
	}
	lc := out.LoggingConfig
	block := &generated.AWSBedrockModelInvocationLoggingConfigurationLoggingConfig{}
	if lc.TextDataDeliveryEnabled != nil {
		block.TextDataDeliveryEnabled = generated.LiteralOf(*lc.TextDataDeliveryEnabled)
	}
	if lc.ImageDataDeliveryEnabled != nil {
		block.ImageDataDeliveryEnabled = generated.LiteralOf(*lc.ImageDataDeliveryEnabled)
	}
	if lc.EmbeddingDataDeliveryEnabled != nil {
		block.EmbeddingDataDeliveryEnabled = generated.LiteralOf(*lc.EmbeddingDataDeliveryEnabled)
	}
	if lc.CloudWatchConfig != nil {
		cwc := &generated.AWSBedrockModelInvocationLoggingConfigurationLoggingConfigCloudwatchConfig{}
		if s := aws.ToString(lc.CloudWatchConfig.LogGroupName); s != "" {
			cwc.LogGroupName = generated.LiteralOf(s)
		}
		if s := aws.ToString(lc.CloudWatchConfig.RoleArn); s != "" {
			cwc.RoleARN = generated.LiteralOf(s)
		}
		if lc.CloudWatchConfig.LargeDataDeliveryS3Config != nil {
			lds3 := &generated.AWSBedrockModelInvocationLoggingConfigurationLoggingConfigCloudwatchConfigLargeDataDeliveryS3Config{}
			if s := aws.ToString(lc.CloudWatchConfig.LargeDataDeliveryS3Config.BucketName); s != "" {
				lds3.BucketName = generated.LiteralOf(s)
			}
			if s := aws.ToString(lc.CloudWatchConfig.LargeDataDeliveryS3Config.KeyPrefix); s != "" {
				lds3.KeyPrefix = generated.LiteralOf(s)
			}
			cwc.LargeDataDeliveryS3Config = lds3
		}
		block.CloudwatchConfig = cwc
	}
	if lc.S3Config != nil {
		s3c := &generated.AWSBedrockModelInvocationLoggingConfigurationLoggingConfigS3Config{}
		if s := aws.ToString(lc.S3Config.BucketName); s != "" {
			s3c.BucketName = generated.LiteralOf(s)
		}
		if s := aws.ToString(lc.S3Config.KeyPrefix); s != "" {
			s3c.KeyPrefix = generated.LiteralOf(s)
		}
		block.S3Config = s3c
	}
	typed.LoggingConfig = block
	return typed
}

// Compile-time assertions: must satisfy both AttributeEnricher and
// ByIDEnricher (Phase 2 contract).
var (
	_ AttributeEnricher = (*bedrockModelInvocationLoggingConfigurationEnricher)(nil)
	_ ByIDEnricher      = (*bedrockModelInvocationLoggingConfigurationEnricher)(nil)
)
