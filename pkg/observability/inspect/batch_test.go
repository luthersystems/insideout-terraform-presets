// Tests for Dispatcher.AWSBatch and Dispatcher.GCPBatch. The lifted
// code carries reliable's spec invariants from #1080: cred-less
// partitioning (list-actions discovery batches don't pay the cred
// fetch), single-creds-fetch for the batch (assertable via stub
// counters), per-sub error containment, MaxBatchSubs cap, drift hook
// fires per failing sub.
//
// Lifted behaviorally from
// reliable/internal/agentapi/aws_inspect_batch_test.go and
// gcp_inspect_batch_test.go; converted from testify to plain testing.
package inspect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestAWSBatch_RejectsEmpty(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{Resolver: &stubResolver{}, Creds: &stubCreds{}}
	_, err := d.AWSBatch(context.Background(), "sess", nil)
	if err == nil || !strings.Contains(err.Error(), "empty_batch") {
		t.Errorf("expected empty_batch error, got %v", err)
	}
}

func TestAWSBatch_RejectsTooMany(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{Resolver: &stubResolver{}, Creds: &stubCreds{}}
	subs := make([]SubRequest, MaxBatchSubs+1)
	for i := range subs {
		subs[i] = SubRequest{Service: "ec2", Action: "list-actions"}
	}
	_, err := d.AWSBatch(context.Background(), "sess", subs)
	if err == nil || !strings.Contains(err.Error(), "too_many_subs") {
		t.Errorf("expected too_many_subs error, got %v", err)
	}
}

// TestAWSBatch_AcceptsExactlyMaxBatchSubs pins the off-by-one
// boundary: a batch of exactly MaxBatchSubs probes must succeed.
// Without this, a regression that flipped the comparison from
// `len(subs) > MaxBatchSubs` to `len(subs) >= MaxBatchSubs` would
// silently reject the maximum-size batches.
func TestAWSBatch_AcceptsExactlyMaxBatchSubs(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{Resolver: &stubResolver{}, Creds: &stubCreds{}}
	subs := make([]SubRequest, MaxBatchSubs)
	for i := range subs {
		subs[i] = SubRequest{Service: "ec2", Action: "list-actions"}
	}
	results, err := d.AWSBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("expected MaxBatchSubs to be accepted, got error: %v", err)
	}
	if len(results) != MaxBatchSubs {
		t.Errorf("got %d results, want %d", len(results), MaxBatchSubs)
	}
}

// TestAWSBatch_AllCredlessSkipsCredFetch is the #1080 spec test: a
// pure list-actions discovery batch must NOT trigger a credential
// fetch. A regression here forces an Oracle round-trip per discovery
// batch — the exact failure mode the cred-less partitioning was
// designed to avoid.
func TestAWSBatch_AllCredlessSkipsCredFetch(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "aws"}
	creds := &stubCreds{}
	d := &Dispatcher{Resolver: resolver, Creds: creds}

	subs := []SubRequest{
		{Service: "ec2", Action: "list-actions"},
		{Service: "rds", Action: "list-metrics"},
		{Service: "", Action: "list-actions"},
	}
	results, err := d.AWSBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if creds.awsCalls.Load() != 0 {
		t.Errorf("CredsProvider.AWS called %d times, want 0 for all-cred-less batch", creds.awsCalls.Load())
	}
	if resolver.calls.Load() != 0 {
		t.Errorf("ProjectResolver called %d times, want 0 for all-cred-less batch", resolver.calls.Load())
	}
	for i, r := range results {
		if !r.OK {
			t.Errorf("sub %d not OK: %+v", i, r)
		}
	}
}

// TestAWSBatch_SingleCredFetchForBatch is the credential-reuse spy
// test from #1080: even with multiple needs-creds subs, the batch
// fetches credentials exactly once. A regression to per-sub fetching
// would inflate the counter by N.
func TestAWSBatch_SingleCredFetchForBatch(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "aws", projectID: "p"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	metrics := &stubMetrics{awsResult: map[string]any{"ok": true}}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: metrics}

	subs := []SubRequest{
		{Service: "rds", Action: "get-metrics"},
		{Service: "ec2", Action: "get-metrics"},
		{Service: "alb", Action: "get-metrics"},
		// Cred-less sub interleaved — must still skip the fetch path.
		{Service: "ec2", Action: "list-actions"},
	}
	results, err := d.AWSBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	if creds.awsCalls.Load() != 1 {
		t.Errorf("CredsProvider.AWS called %d times, want exactly 1 (single-fetch invariant)", creds.awsCalls.Load())
	}
	if resolver.calls.Load() != 1 {
		t.Errorf("ProjectResolver called %d times, want exactly 1", resolver.calls.Load())
	}
	if metrics.awsCalls.Load() != 3 {
		t.Errorf("MetricsFetcher.AWSGet called %d times, want 3 (one per get-metrics sub)", metrics.awsCalls.Load())
	}
	for i, r := range results {
		if !r.OK {
			t.Errorf("sub %d not OK: %+v", i, r)
		}
	}
}

// TestAWSBatch_CredFetchFailureSurfacedPerSub confirms a cred-fetch
// error is NOT a top-level error — it's attached to every needs-creds
// sub as the wrapped envelope, while cred-less subs still execute.
// A regression that returned the cred error at the top level would
// fail discovery batches that are mostly cred-less.
func TestAWSBatch_CredFetchFailureSurfacedPerSub(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "aws", projectID: "p"}
	creds := &stubCreds{awsErr: errors.New("oracle 503")}
	d := &Dispatcher{Resolver: resolver, Creds: creds}

	subs := []SubRequest{
		{Service: "ec2", Action: "list-actions"},          // cred-less, must succeed
		{Service: "rds", Action: "describe-db-instances"}, // needs creds, must fail
	}
	results, err := d.AWSBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if !results[0].OK {
		t.Errorf("cred-less sub should succeed despite cred fetch failure, got %+v", results[0])
	}
	if results[1].OK {
		t.Errorf("needs-creds sub should fail with cred error, got OK=true")
	}
	if !strings.Contains(results[1].Error, "credential_fetch_failed") {
		t.Errorf("sub[1].Error = %q, want substring %q", results[1].Error, "credential_fetch_failed")
	}
}

// TestAWSBatch_ResultsIndexAligned pins the index-alignment invariant.
// The fan-out runner can complete out of order; results[i].Index ==
// i must hold so MCP-side zip-with-subs logic stays correct.
func TestAWSBatch_ResultsIndexAligned(t *testing.T) {
	t.Parallel()
	d := &Dispatcher{Resolver: &stubResolver{}, Creds: &stubCreds{}}
	subs := make([]SubRequest, 10)
	for i := range subs {
		subs[i] = SubRequest{Service: "ec2", Action: "list-actions"}
	}
	results, err := d.AWSBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 10 {
		t.Fatalf("got %d results, want 10", len(results))
	}
	for i, r := range results {
		if r.Index != i {
			t.Errorf("results[%d].Index = %d, want %d", i, r.Index, i)
		}
	}
}

// TestAWSBatch_DriftFiresPerFailingSub confirms the drift hook is
// invoked for each missing-resource error in the batch. Reliable's
// drift state machine treats per-sub drift signals as session-level
// drift; the dispatcher fires one MissingResource call per failing
// sub. Asserts each call carried the right sessionID and a reason
// containing the failing-sub error so a regression that fired drift
// without per-sub context wouldn't slip through (counter-only check
// would).
func TestAWSBatch_DriftFiresPerFailingSub(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "aws", projectID: "p"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	drift := &stubDrift{}
	metrics := &stubMetrics{awsErr: errors.New("DBInstance prod-db not found")}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Drift: drift, Metrics: metrics}

	subs := []SubRequest{
		{Service: "rds", Action: "get-metrics"},
		{Service: "ec2", Action: "get-metrics"},
		{Service: "ec2", Action: "list-actions"}, // healthy cred-less
	}
	_, err := d.AWSBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := drift.snapshotAll()
	if len(calls) != 2 {
		t.Fatalf("DriftReporter called %d times, want 2 (one per failing sub); calls=%+v", len(calls), calls)
	}
	for i, c := range calls {
		if c.sessionID != "sess" {
			t.Errorf("calls[%d].sessionID = %q, want %q", i, c.sessionID, "sess")
		}
		if !strings.Contains(c.reason, "DBInstance prod-db not found") {
			t.Errorf("calls[%d].reason = %q, want substring %q", i, c.reason, "DBInstance prod-db not found")
		}
	}
}

// TestGCPBatch_RejectsCloudMismatch is the GCP-side cloud assertion.
// A mixed-cloud session row that resolves with cloud="aws" must NOT
// run GCP probes against AWS creds — the batch surfaces a structured
// project_lookup_failed per needs-creds sub.
func TestGCPBatch_RejectsCloudMismatch(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "aws", projectID: "p"}
	creds := &stubCreds{}
	d := &Dispatcher{Resolver: resolver, Creds: creds}

	subs := []SubRequest{
		{Service: "compute", Action: "list-instances"},
		{Service: "gcs", Action: "list-actions"}, // cred-less, must succeed
	}
	results, err := d.GCPBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].OK {
		t.Errorf("needs-creds sub on AWS session should fail with cloud-mismatch, got OK=true")
	}
	if !strings.Contains(results[0].Error, "not a GCP deployment") {
		t.Errorf("sub[0].Error = %q, want substring %q", results[0].Error, "not a GCP deployment")
	}
	if !results[1].OK {
		t.Errorf("cred-less sub should succeed, got %+v", results[1])
	}
	if creds.gcpCalls.Load() != 0 {
		t.Errorf("CredsProvider.GCP called %d times, want 0 on cloud-mismatch (no fetch attempt)", creds.gcpCalls.Load())
	}
}

// TestGCPBatch_SingleCredFetchAndOK is the GCP-side credential-reuse
// counterpart to TestAWSBatch_SingleCredFetchForBatch.
func TestGCPBatch_SingleCredFetchAndOK(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "gcp", projectID: "p"}
	creds := &stubCreds{gcpCreds: validGCPCreds()}
	metrics := &stubMetrics{gcpResult: map[string]any{"ok": true}}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: metrics}

	subs := []SubRequest{
		{Service: "compute", Action: "get-metrics"},
		{Service: "gcs", Action: "get-metrics"},
		{Service: "cloudsql", Action: "get-metrics"},
	}
	results, err := d.GCPBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.gcpCalls.Load() != 1 {
		t.Errorf("CredsProvider.GCP called %d times, want 1 (single-fetch invariant)", creds.gcpCalls.Load())
	}
	for i, r := range results {
		if !r.OK {
			t.Errorf("sub %d not OK: %+v", i, r)
		}
	}
}

// TestBatch_WallClockBoundsTotal verifies the DefaultBatchWallClock
// override path: setting the var to a small value bounds the entire
// batch even if individual subs misbehave. This is the same hook
// reliable's wall-clock enforcement test uses.
//
// NOT t.Parallel(): this test mutates the package-level
// DefaultBatchWallClock var, which the other batch tests read. The
// race detector flags concurrent read+write — making this test serial
// is the documented pattern for tests that must mutate package state.
func TestBatch_WallClockBoundsTotal(t *testing.T) {
	// Save and restore the package-level var.
	prev := DefaultBatchWallClock
	DefaultBatchWallClock = 30 // 30ns — basically immediate
	defer func() { DefaultBatchWallClock = prev }()

	resolver := &stubResolver{cloud: "aws", projectID: "p"}
	creds := &stubCreds{awsCreds: validAWSCreds()}
	// Metrics fetcher blocks until ctx.Done — so the only way the
	// sub returns is via the wall-clock-derived per-sub deadline.
	// Without `context.WithTimeout(ctx, DefaultBatchWallClock)` in
	// AWSBatch, the sub would block past the test's t.Deadline()
	// instead of returning a context-canceled SubResult.
	metrics := &blockingMetrics{}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: metrics}

	subs := []SubRequest{{Service: "ec2", Action: "get-metrics"}}
	start := time.Now()
	results, err := d.AWSBatch(context.Background(), "sess", subs)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Index != 0 {
		t.Errorf("results[0].Index = %d, want 0", results[0].Index)
	}
	// With a ~30ns wall clock the sub must NOT succeed — the
	// per-sub deadline derived from the wall clock fires
	// effectively immediately. A regression that removed
	// `context.WithTimeout(ctx, DefaultBatchWallClock)` would block
	// here in blockingMetrics until the test runner's own deadline
	// fires.
	if results[0].OK {
		t.Errorf("results[0].OK = true under near-zero wall clock; wall clock not applied (%+v)", results[0])
	}
	// Defense in depth: if the wall clock is applied, the batch
	// must finish within a few seconds well below any reasonable
	// test runner deadline. 5s is generous for CI jitter.
	if elapsed > 5*time.Second {
		t.Errorf("batch took %v under near-zero wall clock; wall clock effectively not applied", elapsed)
	}
}

// blockingMetrics blocks AWSGet/GCPGet on ctx — so a sub against
// it can only return when the per-sub context is canceled (the
// wall-clock-derived deadline, the per-sub timeout, or caller ctx
// cancel). Used in the wall-clock bounds test to assert the
// dispatcher actually applies DefaultBatchWallClock.
type blockingMetrics struct{}

func (b *blockingMetrics) AWSGet(ctx context.Context, cfg aws.Config, service, filters string) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingMetrics) GCPGet(ctx context.Context, creds *GCPCreds, service, filters string) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingMetrics) ListAWS(service string) any { return nil }
func (b *blockingMetrics) ListGCP(service string) any { return nil }

// TestAWSBatch_PerSubErrorContainment guarantees one erroring sub
// doesn't take down siblings. Sub idx=1 returns an error from the
// MetricsFetcher; siblings (idx=0 and idx=2) must still succeed and
// the failing sub must surface its error in SubResult.Error without
// affecting the others. The runner-level panic recovery is covered
// by TestRunSubsBounded_PanicRecovery; this test pins the
// Dispatcher.AWSBatch end-to-end seam.
func TestAWSBatch_PerSubErrorContainment(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "aws", projectID: "p"}
	creds := &stubCreds{awsCreds: validAWSCreds()}

	// erringMetrics returns OK except for one specific service.
	metrics := &erringMetrics{
		awsResult: map[string]any{"ok": true},
		errFor:    map[string]error{"rds": errors.New("rds outage")},
	}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Metrics: metrics}

	subs := []SubRequest{
		{Service: "ec2", Action: "get-metrics"},
		{Service: "rds", Action: "get-metrics"},
		{Service: "alb", Action: "get-metrics"},
	}
	results, err := d.AWSBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !results[0].OK || !results[2].OK {
		t.Errorf("sibling subs should be OK, got results[0]=%+v results[2]=%+v", results[0], results[2])
	}
	if results[1].OK {
		t.Errorf("erroring sub should not be OK: %+v", results[1])
	}
	if !strings.Contains(results[1].Error, "rds outage") {
		t.Errorf("results[1].Error = %q, want substring %q", results[1].Error, "rds outage")
	}
}

// erringMetrics is a per-service erroring MetricsFetcher used by
// TestAWSBatch_PerSubErrorContainment. Other tests should keep using
// stubMetrics.
type erringMetrics struct {
	awsResult any
	errFor    map[string]error // keyed by service
}

func (e *erringMetrics) AWSGet(ctx context.Context, cfg aws.Config, service, filters string) (any, error) {
	if err, ok := e.errFor[service]; ok {
		return nil, err
	}
	return e.awsResult, nil
}

func (e *erringMetrics) GCPGet(ctx context.Context, creds *GCPCreds, service, filters string) (any, error) {
	if err, ok := e.errFor[service]; ok {
		return nil, err
	}
	return e.awsResult, nil
}

func (e *erringMetrics) ListAWS(service string) any { return nil }
func (e *erringMetrics) ListGCP(service string) any { return nil }

// TestGCPBatch_DriftFiresPerFailingSub mirrors TestAWSBatch's drift
// hook coverage on the GCP path.
func TestGCPBatch_DriftFiresPerFailingSub(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{cloud: "gcp", projectID: "p"}
	creds := &stubCreds{gcpCreds: validGCPCreds()}
	drift := &stubDrift{}
	metrics := &stubMetrics{gcpErr: errors.New("topic not found")}
	d := &Dispatcher{Resolver: resolver, Creds: creds, Drift: drift, Metrics: metrics}

	subs := []SubRequest{
		{Service: "pubsub", Action: "get-metrics"},
		{Service: "gcs", Action: "get-metrics"},
	}
	_, err := d.GCPBatch(context.Background(), "sess", subs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if drift.calls.Load() != 2 {
		t.Errorf("DriftReporter called %d times, want 2", drift.calls.Load())
	}
}
