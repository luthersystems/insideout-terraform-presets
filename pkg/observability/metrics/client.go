package metrics

import (
	"context"
	"fmt"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
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

// NewClients builds a Clients value bound to region. Loads ambient
// credentials via config.LoadDefaultConfig — appropriate for callers
// that run inside the same trust boundary as the resources they're
// fetching metrics for. Callers that already hold a resolved aws.Config
// (reliable's broker-issued assumed-role config, integration-test
// configs built via STS AssumeRole, etc.) should use NewClientsFromConfig
// instead so the resolved credentials flow through unchanged.
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

// NewClientsFromConfig builds a Clients value from an already-resolved
// aws.Config. Use this when the caller has obtained credentials through
// a path other than ambient default-config resolution — e.g. reliable's
// Oracle credential-broker assumed-role flow, or an integration test
// that built a config via sts.AssumeRole.
//
// Region is taken from cfg.Region; if cfg.Region is empty an error is
// returned (the caller should set it explicitly via
// config.WithRegion(...) when building cfg). The supplied cfg is also
// retained as baseCfg so the lazy CloudFront us-east-1 client can clone
// the resolved credentials without reissuing them.
func NewClientsFromConfig(cfg aws.Config) (*Clients, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("metrics: cfg.Region is required")
	}
	return &Clients{
		Region:     cfg.Region,
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

// MonitoringAPI is the subset of the Cloud Monitoring v3 client the
// metric-fetch path actually invokes. Mirrors reliable's GCPMonitoringAPI
// (gcp_metrics.go:50). Narrowed to ListTimeSeries because that's the
// only Cloud Monitoring op FetchGCP issues — alert policies and other
// surfaces live in the discovery dispatchers (C15) and don't share this
// interface. Kept as an interface so tests can inject a fake without
// standing up the full monitoring client.
//
// The real client returns a server-streaming iterator; this interface
// forces the implementation to drain it to a slice up front. That's
// fine for the metric-fetch path — Cloud Monitoring's ListTimeSeries
// caps results well below memory pressure for typical
// (project, metric type) windows.
type MonitoringAPI interface {
	ListTimeSeries(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error)
}

// realMonitoringClient wraps the Cloud Monitoring metric client to drain
// its iterator into a slice. Mirrors reliable's realGCPMonitoringClient
// (gcp_metrics.go:55). Kept unexported — callers construct one via
// NewGCPClients.
type realMonitoringClient struct {
	client *monitoring.MetricClient
}

func (r *realMonitoringClient) ListTimeSeries(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
	it := r.client.ListTimeSeries(ctx, req)
	var results []*monitoringpb.TimeSeries
	for {
		ts, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		results = append(results, ts)
	}
	return results, nil
}

// GCPClients bundles the GCP service clients the metric-fetch path
// needs. Today that's just the Cloud Monitoring metric client — the
// per-service discovery clients (Compute, Cloud Run, Functions, …) used
// by reliable's GCP discoverers land in C15 alongside the GCP inspector
// port. We give callers a Clients struct anyway so the C15 expansion is
// additive: new fields slot in without changing the public constructor
// signature.
//
// Kept separate from Clients (the AWS bundle) because the construction
// paths diverge: AWS uses LoadDefaultConfig(WithRegion) and binds to a
// region; GCP uses option.ClientOption with project-scoped credentials
// and binds to a project. Folding them into one struct would force
// every caller to supply both even when only one cloud is in scope.
type GCPClients struct {
	// ProjectID is the GCP project the Monitoring client is scoped to.
	// Used by FetchGCP to build the "projects/<id>" parent path on
	// every ListTimeSeries request.
	ProjectID string

	// Monitoring is the Cloud Monitoring v3 client used to issue
	// ListTimeSeries calls. Replaceable via the MonitoringAPI interface
	// for tests.
	Monitoring MonitoringAPI

	// closer carries the underlying *monitoring.MetricClient so Close()
	// can release the gRPC connection. nil when Monitoring was injected
	// directly (tests).
	closer *monitoring.MetricClient
}

// NewGCPClients builds a GCPClients value bound to projectID. Mirrors
// reliable's getGCPServiceMetrics inline construction (gcp_metrics.go:378-389).
// Ambient credentials only — Application Default Credentials. The Oracle
// service-account-token machinery in reliable lives outside the
// metric-fetch core and is not needed here; callers wanting to pass
// scoped credentials can inject MonitoringAPI directly.
//
// Variadic option.ClientOption is the natural extension point for
// callers that need to override credentials, endpoint, or quota project
// — same shape as monitoring.NewMetricClient itself.
func NewGCPClients(ctx context.Context, projectID string, opts ...option.ClientOption) (*GCPClients, error) {
	if projectID == "" {
		return nil, fmt.Errorf("metrics: projectID is required")
	}
	client, err := monitoring.NewMetricClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("metrics: create monitoring client: %w", err)
	}
	return &GCPClients{
		ProjectID:  projectID,
		Monitoring: &realMonitoringClient{client: client},
		closer:     client,
	}, nil
}

// Close releases the underlying gRPC connection. Safe to call on a
// GCPClients with an injected MonitoringAPI (no-op in that case).
func (c *GCPClients) Close() error {
	if c == nil || c.closer == nil {
		return nil
	}
	return c.closer.Close()
}
