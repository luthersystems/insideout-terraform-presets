package metrics

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
)

// CloudWatchAPI is the subset of the CloudWatch v2 SDK client the
// metric-fetch path actually invokes. Mirrors reliable's CloudWatchAPI
// (aws_metrics.go:58). Narrowed to GetMetricData because that's the only
// CloudWatch op Fetch issues — every other CloudWatch surface (alarms,
// dashboards, etc.) lives elsewhere. Kept as an interface so tests can
// inject a fake without standing up the full SDK client.
type CloudWatchAPI interface {
	GetMetricData(ctx context.Context, params *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
}

// Clients bundles the AWS service clients the metric-fetch path needs.
// Today that's just CloudWatch — the per-service discovery clients
// (EC2, RDS, Lambda, …) used by reliable's discoverers land in C14
// alongside the inspector port. We give callers a Clients struct anyway
// so the C14 expansion is additive: new fields slot in without changing
// the public constructor signature.
//
// CloudFront is the one quirk: AWS only publishes AWS/CloudFront
// metrics in us-east-1, regardless of where the distribution lives, so
// CloudWatchUSEast1 is a separate client pinned to that region. It's
// lazy — created on first access via cloudFrontClient() — so a stack
// with no CloudFront component never pays the second LoadDefaultConfig.
type Clients struct {
	// Region the primary CloudWatch client is bound to.
	Region string

	// CloudWatch is the region-bound client used for every service
	// except CloudFront.
	CloudWatch CloudWatchAPI

	// baseCfg is retained so cloudFrontClient() can re-derive a
	// us-east-1-pinned config without re-reading ambient credentials
	// (env / shared config / IMDS) — those are already resolved on
	// baseCfg.Credentials and we just clone them onto the new region.
	baseCfg aws.Config

	// cloudFrontCW is created lazily by cloudFrontClient(). nil means
	// not-yet-created; populated on first call.
	cloudFrontCW CloudWatchAPI
}

// NewClients builds a Clients value bound to region. Mirrors the
// LoadDefaultConfig path used by reliable's getServiceMetrics
// (aws_metrics.go:614) — ambient credentials only. STS-AssumeRole
// machinery in reliable is integration-test scaffolding (see
// aws_metrics_test.go:125 integrationAWSConfig); not part of the
// metric-fetch production path, so not ported.
func NewClients(ctx context.Context, region string) (*Clients, error) {
	if region == "" {
		return nil, fmt.Errorf("metrics: region is required")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("metrics: load default config: %w", err)
	}
	return &Clients{
		Region:     region,
		CloudWatch: cloudwatch.NewFromConfig(cfg),
		baseCfg:    cfg,
	}, nil
}

// cloudFrontClient returns a CloudWatch client pinned to us-east-1,
// reusing the base config's resolved credentials. Mirrors reliable's
// createCloudFrontMetricsConfig (aws_metrics.go:1975). Lazy: only the
// first call pays for credentials.Retrieve + LoadDefaultConfig.
//
// Concurrent first-call races are a non-issue today — the metric-fetch
// pipeline is sequential per (component, region). Add a sync.Once if
// that ever changes.
func (c *Clients) cloudFrontClient(ctx context.Context) (CloudWatchAPI, error) {
	if c.cloudFrontCW != nil {
		return c.cloudFrontCW, nil
	}
	creds, err := c.baseCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("metrics: retrieve base credentials: %w", err)
	}
	usEast1Cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID,
			creds.SecretAccessKey,
			creds.SessionToken,
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: load us-east-1 config for cloudfront: %w", err)
	}
	c.cloudFrontCW = cloudwatch.NewFromConfig(usEast1Cfg)
	return c.cloudFrontCW, nil
}
