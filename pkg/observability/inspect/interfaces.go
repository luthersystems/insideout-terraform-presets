// internal/agentapi/aws_inspect.go and gcp_inspect.go in
// luthersystems/reliable resolve a session ID to credentials via two
// concrete dependencies: the tf_runs DB row (project + cloud lookup) and
// the Oracle credential broker (HTTP). Reliable also runs a drift
// bookkeeping side-effect on missing-resource errors and intercepts the
// `get-metrics` action with its own CloudWatch / Cloud Monitoring
// catalog. Lifting the dispatcher into presets requires hiding those
// reliable-internal concerns behind interfaces the caller injects.
//
// This file declares those four seams. Reliable supplies concrete
// implementations in `reliable#1308` (the cutover PR); presets ships
// only the contracts.
package inspect

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// AWSCreds is the resolved-AWS-credentials shape returned by
// CredsProvider.AWS. The dispatcher converts this into an aws.Config via
// the AWS SDK's static-credentials provider; reliable's existing Oracle
// inspector-credentials response decodes directly into this struct.
type AWSCreds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	// Expiration is informational — the dispatcher does not enforce it.
	// Callers fetching creds repeatedly should refresh based on this
	// value or on the broker's cache TTL.
	Expiration string
}

// GCPCreds is the resolved-GCP-credentials shape returned by
// CredsProvider.GCP. AccessToken is the OAuth 2.0 token used by the
// Cloud SDK clients; ProjectID is the GCP project id targeted (which
// may differ from the reliable-session "project name" used for filter
// scoping — see ProjectResolver doc).
type GCPCreds struct {
	AccessToken string
	ProjectID   string
	Expiration  string
}

// ProjectResolver maps a reliable session ID to (projectID, cloud).
// Reliable implements this against its tf_runs table; presets ships
// only the interface so the dispatcher does not depend on a database.
//
// projectID is the cloud-side project identifier (AWS account-id-bearing
// project, or GCP project id). cloud is "aws" or "gcp"; an empty string
// is treated as "aws" by AWS callers (legacy parity with reliable's
// pre-multi-cloud session rows).
type ProjectResolver interface {
	ResolveSession(ctx context.Context, sessionID string) (projectID, cloud string, err error)
}

// CredsProvider fetches inspector credentials for a project. Errors
// SHOULD wrap *CredentialFetchError so callers can errors.As() into
// the structured envelope (used by reliable's component-metrics
// handler to render a categorized retry envelope to the UI).
type CredsProvider interface {
	AWS(ctx context.Context, projectID string) (*AWSCreds, error)
	GCP(ctx context.Context, projectID string) (*GCPCreds, error)
}

// DriftReporter is invoked when the dispatcher classifies an inspect
// error as a missing-resource (see IsMissingResource). Reliable
// implements this against its session-meta drift state; presets ships
// only the interface. Optional — a Dispatcher with nil Drift simply
// skips the side-effect.
type DriftReporter interface {
	MissingResource(ctx context.Context, sessionID, reason, componentKey string)
}

// MetricsFetcher handles the special `get-metrics` action and the
// cred-less `list-metrics` discovery action. Neither is routed through
// pkg/observability/discovery/{aws,gcp}.Inspect (the upstream AWS
// dispatcher returns ErrUseMetricsPackage; the GCP dispatcher does
// not route get-metrics). Reliable injects its CloudWatch /
// Cloud Monitoring catalog here so the lifted dispatcher does not
// have to own a per-service metrics catalog.
//
// Optional — a Dispatcher with nil Metrics returns
// "metrics fetcher not configured" on `get-metrics`, and a stub
// `{service, metrics: [], note}` shape on `list-metrics`. Reliable's
// existing `listAvailableMetrics` / `listAvailableGCPMetrics` are
// the byte-equal implementations callers should wire in.
type MetricsFetcher interface {
	AWSGet(ctx context.Context, cfg aws.Config, service, filters string) (any, error)
	GCPGet(ctx context.Context, creds *GCPCreds, service, filters string) (any, error)
	// ListAWS returns the per-service AWS metrics catalog (no
	// credentials needed). Mirrors reliable's
	// listAvailableMetrics(service) at aws_metrics.go:131. Wire
	// shape: a struct with `namespace`, `metrics: [...]`, `dimension_*`,
	// and `note` keys per the reliable contract — pass-through to
	// the existing reliable implementation.
	ListAWS(service string) any
	// ListGCP is the GCP twin of ListAWS. Mirrors reliable's
	// listAvailableGCPMetrics(service) at gcp_metrics.go:91.
	ListGCP(service string) any
}
