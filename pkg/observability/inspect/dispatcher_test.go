// Tests for Dispatcher.AWS and Dispatcher.GCP. Cover the four
// load-bearing seams the lifted dispatcher introduces:
//
//  1. ProjectResolver — session lookup + cloud-mismatch error path
//  2. CredsProvider — credential fetch + CredentialFetchError surfacing
//  3. DriftReporter — fires only on missing-resource errors, optional
//  4. MetricsFetcher — handles `get-metrics`, optional, AWS+GCP both
//
// The cred-less early-return paths (list-actions, list-metrics) are
// also covered here because they short-circuit before any cred fetch
// — a regression there would silently force a credential fetch on
// every list-actions discovery call.
//
// Lifted behaviorally from reliable/internal/agentapi/{aws,gcp}_inspect_*_test.go;
// converted from testify to plain testing.

package inspect

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// stubResolver is a hand-rolled ProjectResolver fake. Pattern matches
// the existing presets fakes in pkg/observability/discovery/aws/auth.go.
type stubResolver struct {
	projectID string
	cloud     string
	err       error
	calls     atomic.Int32
}

func (s *stubResolver) ResolveSession(ctx context.Context, sessionID string) (string, string, error) {
	s.calls.Add(1)
	return s.projectID, s.cloud, s.err
}

type stubCreds struct {
	awsCreds *AWSCreds
	gcpCreds *GCPCreds
	awsErr   error
	gcpErr   error
	awsCalls atomic.Int32
	gcpCalls atomic.Int32
}

func (s *stubCreds) AWS(ctx context.Context, projectID string) (*AWSCreds, error) {
	s.awsCalls.Add(1)
	return s.awsCreds, s.awsErr
}

func (s *stubCreds) GCP(ctx context.Context, projectID string) (*GCPCreds, error) {
	s.gcpCalls.Add(1)
	return s.gcpCreds, s.gcpErr
}

type stubDrift struct {
	calls atomic.Int32
	mu    sync.Mutex
	last  struct {
		sessionID    string
		reason       string
		componentKey string
	}
}

func (s *stubDrift) MissingResource(ctx context.Context, sessionID, reason, componentKey string) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last.sessionID = sessionID
	s.last.reason = reason
	s.last.componentKey = componentKey
}

// snapshotLast returns a copy of the most recent MissingResource args
// under the mutex. Tests must use this rather than reading
// s.last.* directly.
func (s *stubDrift) snapshotLast() (sessionID, reason, componentKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last.sessionID, s.last.reason, s.last.componentKey
}

type stubMetrics struct {
	awsResult any
	gcpResult any
	awsErr    error
	gcpErr    error
	awsCalls  atomic.Int32
	gcpCalls  atomic.Int32
}

func (s *stubMetrics) AWSGet(ctx context.Context, cfg aws.Config, service, filters string) (any, error) {
	s.awsCalls.Add(1)
	return s.awsResult, s.awsErr
}

func (s *stubMetrics) GCPGet(ctx context.Context, creds *GCPCreds, service, filters string) (any, error) {
	s.gcpCalls.Add(1)
	return s.gcpResult, s.gcpErr
}

func validAWSCreds() *AWSCreds {
	return &AWSCreds{
		AccessKeyID:     "AKIA-TEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Region:          "us-east-1",
	}
}

func validGCPCreds() *GCPCreds {
	return &GCPCreds{
		AccessToken: "ya29.test",
		ProjectID:   "test-project",
	}
}

// TestDispatcherAWS_ListActionsCredless confirms list-actions short-
// circuits before any cred fetch. A regression here would force an
// Oracle round-trip for every discovery batch — exactly the failure
// mode the cred-less path was designed to avoid.
func TestDispatcherAWS_ListActionsCredless(t *testing.T) {
	t.Parallel()
	creds := &stubCreds{}
	resolver := &stubResolver{cloud: "aws"}
	d := &Dispatcher{Resolver: resolver, Creds: creds}

	got, err := d.AWS(context.Background(), "sess", "ec2", "list-actions", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.awsCalls.Load() != 0 {
		t.Errorf("CredsProvider.AWS called %d times, want 0 for cred-less list-actions", creds.awsCalls.Load())
	}
	if resolver.calls.Load() != 0 {
		t.Errorf("ProjectResolver called %d times, want 0 for cred-less list-actions", resolver.calls.Load())
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", got)
	}
	if m["service"] != "ec2" {
		t.Errorf("result[service] = %v, want %q", m["service"], "ec2")
	}
	if _, ok := m["actions"].([]string); !ok {
		t.Errorf("result[actions] = %T, want []string", m["actions"])
	}
}

// TestDispatcherAWS_ListActionsEmptyServiceListsAll pins the
// service=="" case → returns the full per-cloud service list. Mirrors
// reliable's aws_inspect.go:110-113.
func TestDispatcherAWS_ListActionsEmptyServiceListsAll(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{Resolver: &stubResolver{}, Creds: &stubCreds{}}
	got, err := d.AWS(context.Background(), "sess", "", "list-actions", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", got)
	}
	services, ok := m["services"].([]string)
	if !ok || len(services) == 0 {
		t.Errorf("result[services] = %v, want non-empty []string", m["services"])
	}
}

// TestDispatcherAWS_ListActionsUnknownService confirms unknown service
// surfaces an UnsupportedServiceError (not an empty result).
func TestDispatcherAWS_ListActionsUnknownService(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{Resolver: &stubResolver{}, Creds: &stubCreds{}}
	_, err := d.AWS(context.Background(), "sess", "made-up-service", "list-actions", "")
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported service") {
		t.Errorf("error = %q, want substring %q", err.Error(), "unsupported service")
	}
}

// TestDispatcherAWS_ProjectLookupFailed wraps the resolver error in
// the load-bearing "project_lookup_failed" prefix. The drift
// classifier's negative-signals list specifically excludes this prefix
// (so a "not found" tail in a project-lookup error doesn't trigger
// drift); this test pins the prefix shape so the classifier stays in
// sync.
func TestDispatcherAWS_ProjectLookupFailed(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{err: errors.New("db read timeout")}
	d := &Dispatcher{Resolver: resolver, Creds: &stubCreds{}}

	_, err := d.AWS(context.Background(), "sess", "ec2", "describe-instances", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "project_lookup_failed:") {
		t.Errorf("error = %q, want prefix %q", err.Error(), "project_lookup_failed:")
	}
	if IsMissingResource(err) {
		t.Error("project_lookup_failed must not classify as missing resource (drift false positive)")
	}
}

// TestDispatcherAWS_CredentialFetchFailedNoDrift confirms a cred-
// fetch failure is wrapped with the credential_fetch_failed prefix
// and does NOT trigger drift bookkeeping. The drift classifier's
// negative-signals list pins this — a credential outage shouldn't
// look like a missing resource.
func TestDispatcherAWS_CredentialFetchFailedNoDrift(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsErr: errors.New("oracle unreachable")}
	drift := &stubDrift{}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Drift: drift}

	_, err := d.AWS(context.Background(), "sess", "ec2", "describe-instances", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "credential_fetch_failed:") {
		t.Errorf("error = %q, want prefix %q", err.Error(), "credential_fetch_failed:")
	}
	if drift.calls.Load() != 0 {
		t.Errorf("DriftReporter called %d times, want 0 on cred-fetch failure", drift.calls.Load())
	}
}

// TestDispatcherGCP_CloudMismatchRejected is the GCP-side check that
// the resolved session's cloud == "gcp". Reliable's session DB has
// AWS rows too; serving them through the GCP dispatch path would
// crash on the first GCP API call. Pin the early reject.
func TestDispatcherGCP_CloudMismatchRejected(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	d := &Dispatcher{Resolver: resolver, Creds: &stubCreds{}}

	_, err := d.GCP(context.Background(), "sess", "compute", "list-instances", "")
	if err == nil {
		t.Fatal("expected cloud-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "not a GCP deployment") {
		t.Errorf("error = %q, want substring %q", err.Error(), "not a GCP deployment")
	}
}

// TestDispatcherGCP_CredentialFetchPreservesEnvelope confirms a
// CredentialFetchError surfaced by the GCP CredsProvider survives the
// dispatcher's wrap (which uses %w, not %v). The component-metrics
// handler relies on errors.As() to render a categorized envelope; a
// %v wrap would break that without a compile error.
func TestDispatcherGCP_CredentialFetchPreservesEnvelope(t *testing.T) {
	t.Parallel()
	envelope := &CredentialFetchError{
		Category:     CredFetchUpstream5xx,
		OracleStatus: 503,
		BodyExcerpt:  "service unavailable",
		Retryable:    true,
	}
	resolver := &stubResolver{projectID: "p", cloud: "gcp"}
	creds := &stubCreds{gcpErr: envelope}
	d := &Dispatcher{Resolver: resolver, Creds: creds}

	_, err := d.GCP(context.Background(), "sess", "compute", "list-instances", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var got *CredentialFetchError
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(*CredentialFetchError) = false on wrapped err %q — %%w wrap broken?", err)
	}
	if got.Category != CredFetchUpstream5xx {
		t.Errorf("category = %q, want %q", got.Category, CredFetchUpstream5xx)
	}
	if got.OracleStatus != 503 {
		t.Errorf("oracle_status = %d, want 503", got.OracleStatus)
	}
}

// TestDispatcherAWS_GetMetricsRoutesToFetcher confirms the
// get-metrics action goes to the optional MetricsFetcher and not to
// the discovery dispatcher. A regression here would surface
// awsdiscovery.ErrUseMetricsPackage to the caller.
func TestDispatcherAWS_GetMetricsRoutesToFetcher(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	metrics := &stubMetrics{awsResult: map[string]any{"datapoints": []any{}}}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: metrics}

	got, err := d.AWS(context.Background(), "sess", "ec2", "get-metrics", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metrics.awsCalls.Load() != 1 {
		t.Errorf("MetricsFetcher.AWSGet called %d times, want 1", metrics.awsCalls.Load())
	}
	if got == nil {
		t.Error("result = nil, want non-nil from MetricsFetcher")
	}
}

// TestDispatcherAWS_GetMetricsWithoutFetcher returns a clear "not
// configured" error when MetricsFetcher is nil. Required so the lift
// works in presets-only callers (no metrics catalog available) without
// a nil-deref panic.
func TestDispatcherAWS_GetMetricsWithoutFetcher(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	d := &Dispatcher{Resolver: resolver, Creds: creds}

	_, err := d.AWS(context.Background(), "sess", "ec2", "get-metrics", "")
	if err == nil {
		t.Fatal("expected 'not configured' error, got nil")
	}
	if !strings.Contains(err.Error(), "metrics fetcher not configured") {
		t.Errorf("error = %q, want substring %q", err.Error(), "metrics fetcher not configured")
	}
}

// TestDispatcherGCP_GetMetricsRoutesToFetcher — GCP twin of the AWS
// fetch-routing test.
func TestDispatcherGCP_GetMetricsRoutesToFetcher(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "gcp"}
	creds := &stubCreds{gcpCreds: validGCPCreds()}
	metrics := &stubMetrics{gcpResult: map[string]any{"series": []any{}}}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: metrics}

	got, err := d.GCP(context.Background(), "sess", "compute", "get-metrics", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metrics.gcpCalls.Load() != 1 {
		t.Errorf("MetricsFetcher.GCPGet called %d times, want 1", metrics.gcpCalls.Load())
	}
	if got == nil {
		t.Error("result = nil, want non-nil from MetricsFetcher")
	}
}

// TestDispatcherAWS_DriftReporterFiresOnMissingResource ensures the
// optional DriftReporter is invoked when the discovery layer surfaces
// a "resource not found" error. The componentKey is "" because the
// dispatcher doesn't carry component context — reliable's drift state
// machine treats "" as session-level drift.
func TestDispatcherAWS_DriftReporterFiresOnMissingResource(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	drift := &stubDrift{}

	// Use a MetricsFetcher that returns a missing-resource error so
	// the test goes through the dispatcher without an actual AWS SDK
	// call (no real credentials needed).
	metrics := &stubMetrics{awsErr: errors.New("DBInstance prod-db not found")}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Drift: drift, Metrics: metrics}

	_, err := d.AWS(context.Background(), "sess-42", "rds", "get-metrics", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if drift.calls.Load() != 1 {
		t.Fatalf("DriftReporter called %d times, want 1 on missing-resource", drift.calls.Load())
	}
	gotSessionID, gotReason, gotComponentKey := drift.snapshotLast()
	if gotSessionID != "sess-42" {
		t.Errorf("drift sessionID = %q, want %q", gotSessionID, "sess-42")
	}
	if gotComponentKey != "" {
		t.Errorf("drift componentKey = %q, want empty (session-level drift)", gotComponentKey)
	}
	if !strings.Contains(gotReason, "not found") {
		t.Errorf("drift reason = %q, want contains %q", gotReason, "not found")
	}
}

// TestDispatcherAWS_DriftReporterSilentOnNonDriftErrors covers the
// negative-signals classifier path. A throttled error must NOT trigger
// drift bookkeeping — reliable's clear-drift state machine relies on
// drift signals being deterministic resource-presence hints, not
// wrappers around transient SDK errors.
func TestDispatcherAWS_DriftReporterSilentOnNonDriftErrors(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	drift := &stubDrift{}
	metrics := &stubMetrics{awsErr: errors.New("ThrottlingException: rate exceeded")}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Drift: drift, Metrics: metrics}

	_, err := d.AWS(context.Background(), "sess", "rds", "get-metrics", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if drift.calls.Load() != 0 {
		t.Errorf("DriftReporter called %d times, want 0 on transient error", drift.calls.Load())
	}
}

// TestDispatcherAWS_NilDriftIsSafe confirms a missing-resource error
// with Drift==nil is a no-op. Without this the Dispatcher would force
// every caller to supply a DriftReporter.
func TestDispatcherAWS_NilDriftIsSafe(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	metrics := &stubMetrics{awsErr: errors.New("DBInstance prod-db not found")}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: metrics} // Drift: nil

	_, err := d.AWS(context.Background(), "sess", "rds", "get-metrics", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestDispatcherAWS_ProjectFilterInjected verifies the project-filter
// chain runs when a ProjectNameForFilter is configured. Asserts the
// filter is passed through to the MetricsFetcher (the easiest
// observation point in tests; the discovery dispatcher requires real
// creds).
func TestDispatcherAWS_ProjectFilterInjected(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}

	var seenFilter string
	metrics := &stubMetrics{}
	wrapped := &filterCapturingMetrics{inner: metrics, capture: &seenFilter}
	d := &Dispatcher{
		Resolver:             resolver,
		Creds:                creds,
		Metrics:              wrapped,
		ProjectNameForFilter: func(string) string { return "io-test-stack" },
	}

	if _, err := d.AWS(context.Background(), "sess", "rds", "get-metrics", `{"hours":6}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(seenFilter, `"project":"io-test-stack"`) {
		t.Errorf("filters seen by metrics = %q, want substring %q", seenFilter, `"project":"io-test-stack"`)
	}
}

// TestDispatcherAWS_ProjectFilterSkippedForDemo confirms the "demo"
// project name is treated as "no project filter" (matches reliable's
// ensureProjectFilter behavior).
func TestDispatcherAWS_ProjectFilterSkippedForDemo(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	var seenFilter string
	wrapped := &filterCapturingMetrics{inner: &stubMetrics{}, capture: &seenFilter}
	d := &Dispatcher{
		Resolver:             resolver,
		Creds:                creds,
		Metrics:              wrapped,
		ProjectNameForFilter: func(string) string { return "demo" },
	}
	in := `{"hours":6}`
	if _, err := d.AWS(context.Background(), "sess", "rds", "get-metrics", in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenFilter != in {
		t.Errorf("filter rewritten for demo: got %q, want unchanged %q", seenFilter, in)
	}
}

// TestDispatcherAWS_ProjectFilterNilHookPassesThrough confirms a nil
// ProjectNameForFilter leaves filters unchanged.
func TestDispatcherAWS_ProjectFilterNilHookPassesThrough(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{projectID: "p", cloud: "aws"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	var seenFilter string
	wrapped := &filterCapturingMetrics{inner: &stubMetrics{}, capture: &seenFilter}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: wrapped} // ProjectNameForFilter: nil

	in := `{"hours":6}`
	if _, err := d.AWS(context.Background(), "sess", "rds", "get-metrics", in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenFilter != in {
		t.Errorf("filter rewritten with nil hook: got %q, want unchanged %q", seenFilter, in)
	}
}

// TestDispatcherAWS_NormalizeAction confirms underscore→hyphen
// normalization runs at the entry point. Mirrors reliable's
// normalizeAction at inspect_normalize.go:11.
func TestDispatcherAWS_NormalizeAction(t *testing.T) {
	t.Parallel()
	creds := &stubCreds{}
	d := &Dispatcher{Resolver: &stubResolver{}, Creds: creds}

	got, err := d.AWS(context.Background(), "sess", "ec2", "list_actions", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any (normalizeAction should let list-actions match)", got)
	}
	if m["service"] != "ec2" {
		t.Errorf("result[service] = %v, want %q (normalizeAction or canonicalAWSService broken)", m["service"], "ec2")
	}
}

// filterCapturingMetrics wraps a stubMetrics so test cases can
// observe the filter string the dispatcher passed in.
type filterCapturingMetrics struct {
	inner   *stubMetrics
	capture *string
}

func (f *filterCapturingMetrics) AWSGet(ctx context.Context, cfg aws.Config, service, filters string) (any, error) {
	*f.capture = filters
	return f.inner.AWSGet(ctx, cfg, service, filters)
}

func (f *filterCapturingMetrics) GCPGet(ctx context.Context, creds *GCPCreds, service, filters string) (any, error) {
	*f.capture = filters
	return f.inner.GCPGet(ctx, creds, service, filters)
}

// Compile-time assertion that the helper satisfies MetricsFetcher.
var _ MetricsFetcher = (*filterCapturingMetrics)(nil)

// TestDispatcher_NormalizeActionVisibleToTests is a sanity guard that
// the package-private helper signature matches what the lifted
// reliable code expects. Useful when reliable's cutover PR rebases
// onto this package — keeps the contract obvious in one spot.
func TestDispatcher_NormalizeActionVisibleToTests(t *testing.T) {
	t.Parallel()
	if got := normalizeAction("describe_instances"); got != "describe-instances" {
		t.Errorf("normalizeAction = %q, want %q", got, "describe-instances")
	}
	if got := normalizeAction("already-hyphenated"); got != "already-hyphenated" {
		t.Errorf("normalizeAction = %q, want %q (idempotent)", got, "already-hyphenated")
	}
}
