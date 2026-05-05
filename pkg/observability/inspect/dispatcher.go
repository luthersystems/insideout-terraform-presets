// Dispatcher is the session-aware AWS / GCP inspect entry point.
// Session ID → cred resolution → discovery dispatch → drift bookkeeping
// is consolidated here so reliable's HTTP handlers and the future MCP
// extraction in luthersystems/insideout-agent-skills can share one
// implementation.
//
// The implementation lifts
// reliable/internal/agentapi/aws_inspect.go::InspectAWS and
// gcp_inspect.go::InspectGCP, replacing the four reliable-internal
// concerns with the four interface seams declared in interfaces.go:
//
//   - tf_runs DB row → ProjectResolver
//   - Oracle credential broker → CredsProvider
//   - markSessionDriftDetected → DriftReporter (optional)
//   - getServiceMetrics / getGCPServiceMetrics → MetricsFetcher (optional)
//
// Free functions in pkg/observability/discovery/{aws,gcp} (the
// per-cloud Inspect callable) remain unchanged. Callers that already
// hold a resolved aws.Config / GCP project + token continue to use
// those directly. Dispatcher is purely additive.
package inspect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
	awsdiscovery "github.com/luthersystems/insideout-terraform-presets/pkg/observability/discovery/aws"
	gcpdiscovery "github.com/luthersystems/insideout-terraform-presets/pkg/observability/discovery/gcp"
	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// gcpOperationFailedFmt wraps upstream GCP inspection errors. %w (not
// %v) is load-bearing: typed envelopes the upstream layer returns —
// e.g. *observability.GCPFeatureNotEnabledError (presets #245 —
// Identity Platform multi-tenancy) — must survive this wrap so the
// reliable panel renderer can errors.As on them.
const gcpOperationFailedFmt = "gcp_operation_failed: %w"

// Dispatcher resolves a session ID to credentials via injected
// interfaces and delegates discovery dispatch to
// pkg/observability/discovery/{aws,gcp}.Inspect. Methods are safe to
// call concurrently — Dispatcher carries no per-call state and the
// injected interfaces are expected to be thread-safe.
type Dispatcher struct {
	// Resolver maps session IDs to (projectID, cloud). Required.
	Resolver ProjectResolver
	// Creds fetches inspector credentials per project. Required.
	Creds CredsProvider
	// Drift, when non-nil, is invoked on missing-resource errors.
	// Reliable supplies its session-meta drift writer; presets-side
	// callers without drift state pass nil.
	Drift DriftReporter
	// Metrics, when non-nil, handles the `get-metrics` action.
	// Reliable supplies its CloudWatch / Cloud Monitoring fetch path
	// (with the per-service catalog); presets-side callers pass nil
	// and `get-metrics` returns "not configured".
	Metrics MetricsFetcher
	// ProjectNameForFilter, when non-nil, derives the observability
	// filter project value from a session ID. Used to inject a
	// `Project=<name>` tag/label filter into per-sub `filters` JSON
	// when the caller didn't supply one. Returning "" or "demo"
	// signals "no project filter" — the dispatcher leaves filters
	// unchanged. Reliable supplies projectNameFromSession; callers
	// that already pre-inject the filter pass nil.
	ProjectNameForFilter func(sessionID string) string

	// AWSConfigOptions, when non-nil, returns optional config.LoadOptions
	// the dispatcher passes to config.LoadDefaultConfig in addition to
	// the static credentials and region from the resolved AWSCreds.
	// Most callers leave this nil; tests can use it to inject custom
	// HTTP transports.
	AWSConfigOptions func() []func(*config.LoadOptions) error
}

// AWS performs an AWS inspect for a single sub. Mirrors reliable's
// InspectAWS contract: action canonicalization, list-actions /
// list-metrics cred-less early returns, project filter injection,
// per-error drift classification.
func (d *Dispatcher) AWS(ctx context.Context, sessionID, service, action, filters string) (any, error) {
	service = normalizeAction(service)
	action = normalizeAction(action)
	service = observability.CanonicalAWSService(service)
	action = observability.CanonicalAWSAction(service, action)

	// Cred-less early exits — match the batch dispatcher's contract.
	if action == "list-metrics" {
		return d.listAWSMetricsResponse(service)
	}
	if action == "list-actions" {
		return listActionsResponse(service, observability.AWSServiceActions, observability.AWSServiceNames())
	}

	projectID, _, err := d.Resolver.ResolveSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("project_lookup_failed: %v", err)
	}
	// AWS path does NOT assert cloud on the resolved session — reliable's
	// pre-multi-cloud rows have no cloud column and default to AWS, and
	// the resolver is expected to return "" for those.

	creds, err := d.Creds.AWS(ctx, projectID)
	if err != nil {
		// %w (not %v) so callers can errors.As() into typed
		// envelopes the CredsProvider may wrap (today reliable's
		// AWS path returns plain errors, but symmetry with the GCP
		// path future-proofs against an AWS-side switch to
		// *CredentialFetchError).
		return nil, fmt.Errorf("credential_fetch_failed: %w", err)
	}

	cfg, err := d.awsConfig(ctx, creds)
	if err != nil {
		return nil, fmt.Errorf("aws_config_failed: %v", err)
	}

	filters = d.injectProjectFilter(filters, sessionID)

	result, err := d.inspectAWSCore(ctx, cfg, service, action, filters)
	if err != nil {
		d.maybeReportDrift(ctx, sessionID, err)
		return nil, err
	}
	return result, nil
}

// GCP performs a GCP inspect for a single sub. Mirrors reliable's
// InspectGCP contract; additionally asserts the resolved cloud is
// "gcp" and applies protoNormalize to the result.
func (d *Dispatcher) GCP(ctx context.Context, sessionID, service, action, filters string) (any, error) {
	service = normalizeAction(service)
	action = normalizeAction(action)
	service = observability.CanonicalGCPService(service)

	if action == "list-metrics" {
		return d.listGCPMetricsResponse(service)
	}
	if action == "list-actions" {
		return listActionsResponse(service, observability.GCPServiceActions, observability.GCPServiceNames())
	}

	projectID, cloud, err := d.Resolver.ResolveSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("project_lookup_failed: %v", err)
	}
	if !strings.EqualFold(cloud, "gcp") {
		return nil, fmt.Errorf("project_lookup_failed: session %s is not a GCP deployment (cloud=%s)", sessionID, cloud)
	}

	creds, err := d.Creds.GCP(ctx, projectID)
	if err != nil {
		// Use %w so callers can errors.As() into *CredentialFetchError
		// and build a categorized response envelope without parsing
		// strings.
		return nil, fmt.Errorf("credential_fetch_failed: %w", err)
	}

	filters = d.injectProjectFilter(filters, sessionID)

	result, err := d.inspectGCPCore(ctx, creds, service, action, filters)
	if err != nil {
		d.maybeReportDrift(ctx, sessionID, err)
		return nil, err
	}
	return result, nil
}

// inspectAWSCore is the post-creds dispatch stage shared by AWS and
// AWSBatch. Routes get-metrics through the optional MetricsFetcher;
// every other action goes to awsdiscovery.Inspect.
func (d *Dispatcher) inspectAWSCore(ctx context.Context, cfg aws.Config, service, action, filters string) (any, error) {
	service = observability.CanonicalAWSService(service)
	action = observability.CanonicalAWSAction(service, action)

	if action == "get-metrics" {
		if d.Metrics == nil {
			return nil, fmt.Errorf("aws_operation_failed: metrics fetcher not configured")
		}
		result, err := d.Metrics.AWSGet(ctx, cfg, service, filters)
		if err != nil {
			return nil, fmt.Errorf("aws_operation_failed: %v", err)
		}
		return result, nil
	}

	// awsdiscovery.Inspect returns ErrUseMetricsPackage on its own
	// get-metrics branch (the non-Dispatcher caller path); here we've
	// already short-circuited get-metrics above, so this only catches
	// truly-unexpected get-metrics resolutions (e.g. an alias that
	// canonicalizes to get-metrics post-dispatch).
	result, err := awsdiscovery.Inspect(ctx, cfg, service, action, filters)
	if err != nil {
		if errors.Is(err, awsdiscovery.ErrUseMetricsPackage) && d.Metrics != nil {
			result, err := d.Metrics.AWSGet(ctx, cfg, service, filters)
			if err != nil {
				return nil, fmt.Errorf("aws_operation_failed: %v", err)
			}
			return result, nil
		}
		return nil, fmt.Errorf("aws_operation_failed: %v", err)
	}
	return result, nil
}

// inspectGCPCore is the post-creds dispatch stage shared by GCP and
// GCPBatch. Routes get-metrics through the optional MetricsFetcher;
// every other action goes to gcpdiscovery.Inspect with protoNormalize
// applied to the result.
func (d *Dispatcher) inspectGCPCore(ctx context.Context, creds *GCPCreds, service, action, filters string) (any, error) {
	service = observability.CanonicalGCPService(service)

	if action == "get-metrics" {
		if d.Metrics == nil {
			return nil, fmt.Errorf("gcp_operation_failed: metrics fetcher not configured")
		}
		result, err := d.Metrics.GCPGet(ctx, creds, service, filters)
		if err != nil {
			return nil, fmt.Errorf(gcpOperationFailedFmt, err)
		}
		return result, nil
	}

	opt := gcpClientOption(creds)
	result, err := gcpdiscovery.Inspect(ctx, creds.ProjectID, service, action, filters, opt)
	if err != nil {
		return nil, fmt.Errorf(gcpOperationFailedFmt, err)
	}
	return protoNormalize(result), nil
}

// awsConfig builds an aws.Config from inspector credentials. Mirrors
// reliable's createAWSConfig at aws_inspect.go:223. Caller-supplied
// AWSConfigOptions are appended AFTER the static creds + region; the
// AWS SDK applies LoadOptions last-wins, so caller-supplied options
// (e.g. WithHTTPClient for a test transport, WithRetryer for tighter
// retry policy) override the defaults set above.
func (d *Dispatcher) awsConfig(ctx context.Context, creds *AWSCreds) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(creds.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID,
			creds.SecretAccessKey,
			creds.SessionToken,
		)),
	}
	if d.AWSConfigOptions != nil {
		opts = append(opts, d.AWSConfigOptions()...)
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

// gcpClientOption builds a Cloud SDK client option from inspector
// credentials. Mirrors reliable's createGCPClientOption at
// gcp_inspect.go:572.
func gcpClientOption(creds *GCPCreds) option.ClientOption {
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: creds.AccessToken})
	return option.WithTokenSource(tokenSource)
}

// injectProjectFilter applies the session→project name → filter
// injection chain when a ProjectNameForFilter is configured. Mirrors
// reliable's ensureProjectFilter at project_filter_glue.go:36.
func (d *Dispatcher) injectProjectFilter(filters, sessionID string) string {
	if d.ProjectNameForFilter == nil {
		return filters
	}
	project := d.ProjectNameForFilter(sessionID)
	if project == "" || project == "demo" {
		return filters
	}
	return filter.EnsureProject(filters, project)
}

// maybeReportDrift forwards err to the optional DriftReporter when
// IsMissingResource(err) is true. The componentKey is "" because the
// dispatcher doesn't carry a component-key concept; reliable's drift
// state machine treats "" as session-level drift.
func (d *Dispatcher) maybeReportDrift(ctx context.Context, sessionID string, err error) {
	if d.Drift == nil {
		return
	}
	if !IsMissingResource(err) {
		return
	}
	d.Drift.MissingResource(ctx, sessionID, err.Error(), "")
}

// listAWSMetricsResponse delegates to the optional MetricsFetcher's
// catalog. When MetricsFetcher is nil the dispatcher returns a stub
// shape so presets-only callers without a catalog don't crash; the
// stub is byte-equal-distinct from reliable's catalog so a missing-
// MetricsFetcher misconfig surfaces immediately at the caller.
func (d *Dispatcher) listAWSMetricsResponse(service string) (any, error) {
	if d.Metrics == nil {
		return map[string]any{"service": service, "metrics": []any{}, "note": "list-metrics requires a MetricsFetcher"}, nil
	}
	return d.Metrics.ListAWS(service), nil
}

// listGCPMetricsResponse — see listAWSMetricsResponse.
func (d *Dispatcher) listGCPMetricsResponse(service string) (any, error) {
	if d.Metrics == nil {
		return map[string]any{"service": service, "metrics": []any{}, "note": "list-metrics requires a MetricsFetcher"}, nil
	}
	return d.Metrics.ListGCP(service), nil
}

// listActionsResponse builds the list-actions response: per-service
// action list, or per-cloud service list when service is empty, or
// unsupported-service error when service is non-empty but unknown.
// Mirrors reliable's logic at aws_inspect.go:106-114 and
// gcp_inspect.go:144-152.
//
// Defensively copies the registry's actions slice so a downstream
// caller that mutates the response doesn't aliase the package-level
// AWSServiceActions / GCPServiceActions maps.
func listActionsResponse(service string, registry map[string][]string, allServices []string) (any, error) {
	if actions, ok := registry[service]; ok {
		out := make([]string, len(actions))
		copy(out, actions)
		return map[string]any{"service": service, "actions": out}, nil
	}
	if service == "" {
		return map[string]any{"services": allServices}, nil
	}
	return nil, observability.UnsupportedServiceError(service, allServices)
}

// normalizeAction replaces underscores with hyphens so that both
// "describe_instances" and "describe-instances" are accepted. Lifted
// from reliable's inspect_normalize.go.
func normalizeAction(s string) string {
	return strings.ReplaceAll(s, "_", "-")
}
