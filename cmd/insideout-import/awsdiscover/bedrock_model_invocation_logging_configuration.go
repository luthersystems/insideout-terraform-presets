package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Bucket C, hand-rolled (#466 follow-up): Cloud Control returns
// TypeNotFoundException for AWS::Bedrock::ModelInvocationLoggingConfiguration
// on `aws cloudcontrol get-resource`; the CFN registry does not know the
// type at all on cust2. The native bedrock SDK is the only working path.
//
// The TF type aws_bedrock_model_invocation_logging_configuration is a
// per-region account singleton: each region has zero-or-one configuration.
// Terraform's import id is the region string per the v6.x provider docs
// (website/docs/r/bedrock_model_invocation_logging_configuration.html.markdown).
const (
	bedrockModelInvocationLoggingConfigurationTFType = "aws_bedrock_model_invocation_logging_configuration"
	bedrockModelInvocationLoggingConfigurationSlug   = "bedrock_model_invocation_logging_configuration"
)

// bedrockModelInvocationLoggingConfigurationClient is the narrow subset of
// the bedrock control-plane SDK the singleton discoverer uses.
// GetModelInvocationLoggingConfiguration is unique among bedrock APIs in
// that it takes no input — the API key is implicit (the account + region
// resolved by the SDK client).
type bedrockModelInvocationLoggingConfigurationClient interface {
	GetModelInvocationLoggingConfiguration(ctx context.Context, in *bedrock.GetModelInvocationLoggingConfigurationInput, opts ...func(*bedrock.Options)) (*bedrock.GetModelInvocationLoggingConfigurationOutput, error)
}

type bedrockModelInvocationLoggingConfigurationDiscoverer struct {
	new func(region string) bedrockModelInvocationLoggingConfigurationClient
	// maxConcurrency is kept for constructor-shape parity with the other
	// hand-rolled discoverers but unused: this discoverer issues exactly
	// one SDK call per region with no per-item fan-out.
	maxConcurrency int
}

func newBedrockModelInvocationLoggingConfigurationDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &bedrockModelInvocationLoggingConfigurationDiscoverer{
		new: func(region string) bedrockModelInvocationLoggingConfigurationClient {
			return bedrock.NewFromConfig(cfg, func(o *bedrock.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *bedrockModelInvocationLoggingConfigurationDiscoverer) ResourceType() string {
	return bedrockModelInvocationLoggingConfigurationTFType
}

// Discover walks args.Regions and calls GetModelInvocationLoggingConfiguration
// once per region. Three response shapes are possible against the live API:
//
//   - 200 OK with non-nil LoggingConfig → configured. Emit ONE
//     ImportedResource keyed on the region.
//   - 200 OK with nil LoggingConfig → unconfigured. Emit nothing
//     (not an error — singleton genuinely absent).
//   - ResourceNotFoundException → unconfigured. Emit nothing (older
//     SDK / region behavior may surface absence as a typed not-found
//     instead of a nil payload).
//   - Any other error → ServiceWarn + skip the region. Mirrors the
//     fail-open posture in bedrock_guardrail (transient errors during a
//     multi-region scan should not abort the whole run).
//
// The resource carries no tags (untaggable in AWS provider 6.x — pinned
// in pkg/composer/imported_provenance.go::untaggableAWS), so this
// discoverer does not run the post-fetch tag filter and emits an empty
// (non-nil) Tags map per the #255 contract.
//
// The Project tag filter is also skipped — there are no tags to filter
// on. Operators who scope to one project on a shared account will see
// the same singleton emitted for every project run; downstream Phase 2
// merge logic dedupes on Identity.ImportID (the region) so this is
// load-bearing-correct, not over-emitting.
func (d *bedrockModelInvocationLoggingConfigurationDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = bedrockModelInvocationLoggingConfigurationSlug
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		out, err := client.GetModelInvocationLoggingConfiguration(ctx, &bedrock.GetModelInvocationLoggingConfigurationInput{})
		if err != nil {
			var notFound *bedrocktypes.ResourceNotFoundException
			if errors.As(err, &notFound) {
				// Unconfigured (typed not-found shape). Not an error.
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				continue
			}
			// Any other error: warn and move on. A throttling or
			// AccessDenied response on one region must not abort the
			// whole scan — operator can re-run with --regions to retry
			// once the credential is fixed.
			args.Emitter.ServiceWarn(slug, region, fmt.Sprintf("GetModelInvocationLoggingConfiguration: %v", err))
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			continue
		}
		if out.LoggingConfig == nil {
			// Empty-state shape: success with nil payload. Treat
			// identical to ResourceNotFoundException — no resource.
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			continue
		}

		// Configured. Build the singleton ImportedResource keyed on
		// region. NativeIDs surface the downstream resources the
		// logging config references (log group, S3 bucket) so the
		// importer wizard can present "this is wired to <log-group>"
		// without requiring a second discover pass on those types.
		native := map[string]string{"region": region}
		lc := out.LoggingConfig
		if lc.CloudWatchConfig != nil {
			if lg := aws.ToString(lc.CloudWatchConfig.LogGroupName); lg != "" {
				native["cloud_watch_log_group_name"] = lg
			}
			if roleArn := aws.ToString(lc.CloudWatchConfig.RoleArn); roleArn != "" {
				native["cloud_watch_role_arn"] = roleArn
			}
		}
		if lc.S3Config != nil {
			if b := aws.ToString(lc.S3Config.BucketName); b != "" {
				native["s3_bucket_name"] = b
			}
			if p := aws.ToString(lc.S3Config.KeyPrefix); p != "" {
				native["s3_key_prefix"] = p
			}
		}

		nameHint := region + "-bedrock-logging"
		// Non-nil empty tags map: this resource is untaggable in AWS
		// provider 6.x, but the #255 JSON-shape contract requires a
		// non-nil tags map on the wire.
		tags := map[string]string{}
		imps = append(imps, makeImportedResource(
			book,
			bedrockModelInvocationLoggingConfigurationTFType,
			nameHint,
			region,
			region,
			args.AccountID,
			native,
			tags,
		))
		args.Emitter.ItemFound(slug, region, bedrockModelInvocationLoggingConfigurationTFType, region)
		regionCount++
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves the per-region singleton by region string. The
// terraform import id is the region itself, so id == region for any
// well-formed call. Issues one GetModelInvocationLoggingConfiguration
// call to verify the singleton exists.
func (d *bedrockModelInvocationLoggingConfigurationDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("bedrock_model_invocation_logging_configuration: empty id: %w", ErrNotSupported)
	}
	// The import id IS the region. If the caller passes a different
	// region argument, the id wins — that's the terraform contract.
	if strings.ContainsAny(id, " :/,") {
		return imported.ImportedResource{}, fmt.Errorf("bedrock_model_invocation_logging_configuration: id %q is not a region: %w", id, ErrNotSupported)
	}
	target := id
	client := d.new(target)
	out, err := client.GetModelInvocationLoggingConfiguration(ctx, &bedrock.GetModelInvocationLoggingConfigurationInput{})
	if err != nil {
		var notFound *bedrocktypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_bedrock_model_invocation_logging_configuration %q: %w", target, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetModelInvocationLoggingConfiguration: %w", err)
	}
	if out.LoggingConfig == nil {
		return imported.ImportedResource{}, fmt.Errorf("aws_bedrock_model_invocation_logging_configuration %q: %w", target, ErrNotFound)
	}
	native := map[string]string{"region": target}
	lc := out.LoggingConfig
	if lc.CloudWatchConfig != nil {
		if lg := aws.ToString(lc.CloudWatchConfig.LogGroupName); lg != "" {
			native["cloud_watch_log_group_name"] = lg
		}
		if roleArn := aws.ToString(lc.CloudWatchConfig.RoleArn); roleArn != "" {
			native["cloud_watch_role_arn"] = roleArn
		}
	}
	if lc.S3Config != nil {
		if b := aws.ToString(lc.S3Config.BucketName); b != "" {
			native["s3_bucket_name"] = b
		}
		if p := aws.ToString(lc.S3Config.KeyPrefix); p != "" {
			native["s3_key_prefix"] = p
		}
	}
	nameHint := target + "-bedrock-logging"
	return makeImportedResource(
		addressBook{},
		bedrockModelInvocationLoggingConfigurationTFType,
		nameHint,
		target,
		target,
		accountID,
		native,
		nil,
	), nil
}
