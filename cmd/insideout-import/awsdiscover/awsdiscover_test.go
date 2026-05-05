package awsdiscover

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

type fakeDiscoverer struct {
	t       string
	out     []imported.ImportedResource
	err     error
	called  int
	gotProj string
	gotReg  string
	gotAcct string

	// DiscoverByID wiring (unused by the existing tests, populated by
	// new tests that exercise the dep-chase aggregator path).
	byIDOut   imported.ImportedResource
	byIDErr   error
	byIDCalls []string
}

func (f *fakeDiscoverer) ResourceType() string { return f.t }
func (f *fakeDiscoverer) Discover(_ context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	f.called++
	f.gotProj, f.gotReg, f.gotAcct = project, region, accountID
	return f.out, f.err
}

func (f *fakeDiscoverer) DiscoverByID(_ context.Context, id, _, _ string) (imported.ImportedResource, error) {
	f.byIDCalls = append(f.byIDCalls, id)
	return f.byIDOut, f.byIDErr
}

func ir(addr string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: addr, ImportID: addr},
		Tier:     imported.TierImportedFlat,
		Source:   imported.SourceImporter,
	}
}

func TestDiscoverTypes_DefaultsToAllSupported(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1"), ir("a2")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	got, err := agg.DiscoverTypes(context.Background(), nil, "p", "r", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("len=%d, want 3", len(got))
	}
	if a.called != 1 || b.called != 1 {
		t.Errorf("each discoverer called once; got a=%d b=%d", a.called, b.called)
	}
	if a.gotProj != "p" || a.gotReg != "r" || a.gotAcct != "acc" {
		t.Errorf("project/region/accountID not threaded; got %q/%q/%q", a.gotProj, a.gotReg, a.gotAcct)
	}
}

func TestDiscoverTypes_SelectiveOnlyCallsRequested(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a"}
	b := &fakeDiscoverer{t: "type_b"}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	if _, err := agg.DiscoverTypes(context.Background(), []string{"type_b"}, "p", "r", "acc"); err != nil {
		t.Fatal(err)
	}
	if a.called != 0 {
		t.Errorf("type_a should not have been called; called=%d", a.called)
	}
	if b.called != 1 {
		t.Errorf("type_b should have been called once; called=%d", b.called)
	}
}

func TestDiscoverTypes_UnknownTypeAggregatesAllErrorsBeforeRunning(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a"}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a}}

	_, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "bogus", "also_bogus"}, "p", "r", "acc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "also_bogus") {
		t.Errorf("error should list every unknown type; got: %v", err)
	}
	if a.called != 0 {
		t.Errorf("no discoverer should run when any type is unknown; type_a called=%d", a.called)
	}
}

func TestDiscoverTypes_PropagatesPerDiscovererError(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a", err: errors.New("Throttling")}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a}}

	_, err := agg.DiscoverTypes(context.Background(), nil, "p", "r", "acc")
	if err == nil || !strings.Contains(err.Error(), "type_a") || !strings.Contains(err.Error(), "Throttling") {
		t.Errorf("expected wrapped error mentioning resource type and underlying cause; got: %v", err)
	}
}

func TestSupportedTypes_IsSorted(t *testing.T) {
	t.Parallel()
	agg := &AWSDiscoverer{byType: map[string]Discoverer{
		"type_z": &fakeDiscoverer{t: "type_z"},
		"type_a": &fakeDiscoverer{t: "type_a"},
		"type_m": &fakeDiscoverer{t: "type_m"},
	}}
	got := agg.SupportedTypes()
	want := []string{"type_a", "type_m", "type_z"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("SupportedTypes()[%d]=%q, want %q (sorted)", i, got[i], w)
		}
	}
}

func TestNewAWSDiscoverer_RegistersAllSupportedTypes(t *testing.T) {
	t.Parallel()
	agg := NewAWSDiscoverer(awsDummyConfig())
	got := agg.SupportedTypes()
	want := map[string]bool{
		// Phase 1 (#266).
		"aws_sqs_queue":             false,
		"aws_dynamodb_table":        false,
		"aws_cloudwatch_log_group":  false,
		"aws_secretsmanager_secret": false,
		"aws_lambda_function":       false,
		// Stage 2c3 dep-chase reference types (#271).
		"aws_iam_role":   false,
		"aws_iam_policy": false,
		"aws_kms_key":    false,
		"aws_s3_bucket":  false,
	}
	for _, typ := range got {
		want[typ] = true
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("expected %q to be registered", k)
		}
	}
}

// TestNewAWSDiscoverer_DiscoverByID_DispatchesAndPropagatesErrNotSupported
// pins the aggregator's per-type dispatch contract: registered types
// route to the matching discoverer; unregistered types return
// ErrNotSupported so dep-chase can convert them to warnings.
func TestNewAWSDiscoverer_DiscoverByID_DispatchesAndPropagatesErrNotSupported(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a"}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a}}

	if _, err := agg.DiscoverByID(context.Background(), "type_a", "id-1", "us-east-1", "123"); err != nil {
		t.Fatal(err)
	}
	if len(a.byIDCalls) != 1 || a.byIDCalls[0] != "id-1" {
		t.Errorf("expected DiscoverByID to dispatch to type_a; calls=%v", a.byIDCalls)
	}
	_, err := agg.DiscoverByID(context.Background(), "type_unknown", "id-1", "us-east-1", "123")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("err=%v, want ErrNotSupported for unregistered type", err)
	}
}

// TestNewAWSDiscoverer_AppliesDefaultMaxConcurrency pins that the legacy
// single-arg constructor delegates with DefaultMaxConcurrency rather than
// silently serializing (which would defeat the point of #270). The
// literal-value pin guards the audit-grounded constant: a refactor that
// re-points DefaultMaxConcurrency to 1 must fail this test.
func TestNewAWSDiscoverer_AppliesDefaultMaxConcurrency(t *testing.T) {
	t.Parallel()
	if DefaultMaxConcurrency != 10 {
		t.Errorf("DefaultMaxConcurrency=%d, want 10 (audit-grounded sweet spot per #270 — change requires updating both the constant doc and this pin)", DefaultMaxConcurrency)
	}
	agg := NewAWSDiscoverer(awsDummyConfig())
	dyn, ok := agg.byType["aws_dynamodb_table"].(*dynamoDiscoverer)
	if !ok {
		t.Fatalf("dynamodb discoverer is not *dynamoDiscoverer (got %T)", agg.byType["aws_dynamodb_table"])
	}
	if dyn.maxConcurrency != DefaultMaxConcurrency {
		t.Errorf("dynamo maxConcurrency=%d, want %d", dyn.maxConcurrency, DefaultMaxConcurrency)
	}
	lam, ok := agg.byType["aws_lambda_function"].(*lambdaDiscoverer)
	if !ok {
		t.Fatalf("lambda discoverer is not *lambdaDiscoverer (got %T)", agg.byType["aws_lambda_function"])
	}
	if lam.maxConcurrency != DefaultMaxConcurrency {
		t.Errorf("lambda maxConcurrency=%d, want %d", lam.maxConcurrency, DefaultMaxConcurrency)
	}
}

// TestNewAWSDiscovererWithConcurrency_ThreadsValueToFanoutDiscoverers
// pins that an explicit concurrency value reaches both per-item-fanout
// discoverers (DynamoDB and Lambda). The single-call discoverers (SQS,
// CloudWatch Logs, SecretsManager) ignore the value by design.
func TestNewAWSDiscovererWithConcurrency_ThreadsValueToFanoutDiscoverers(t *testing.T) {
	t.Parallel()
	agg := NewAWSDiscovererWithConcurrency(awsDummyConfig(), 25)
	if d := agg.byType["aws_dynamodb_table"].(*dynamoDiscoverer); d.maxConcurrency != 25 {
		t.Errorf("dynamo maxConcurrency=%d, want 25", d.maxConcurrency)
	}
	if l := agg.byType["aws_lambda_function"].(*lambdaDiscoverer); l.maxConcurrency != 25 {
		t.Errorf("lambda maxConcurrency=%d, want 25", l.maxConcurrency)
	}
}

// TestNewAWSDiscovererWithConcurrency_NonPositiveFallsBackToDefault
// is the safety net for direct programmatic callers. The CLI rejects
// non-positive --max-concurrency upstream, but a Go caller using this
// constructor directly should not get a deadlocked errgroup
// (g.SetLimit(0) blocks forever).
func TestNewAWSDiscovererWithConcurrency_NonPositiveFallsBackToDefault(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1, -100} {
		agg := NewAWSDiscovererWithConcurrency(awsDummyConfig(), n)
		if d := agg.byType["aws_dynamodb_table"].(*dynamoDiscoverer); d.maxConcurrency != DefaultMaxConcurrency {
			t.Errorf("n=%d: dynamo maxConcurrency=%d, want %d", n, d.maxConcurrency, DefaultMaxConcurrency)
		}
		if l := agg.byType["aws_lambda_function"].(*lambdaDiscoverer); l.maxConcurrency != DefaultMaxConcurrency {
			t.Errorf("n=%d: lambda maxConcurrency=%d, want %d", n, l.maxConcurrency, DefaultMaxConcurrency)
		}
	}
}
