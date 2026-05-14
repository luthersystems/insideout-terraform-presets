package awsdiscover

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	smithy "github.com/aws/smithy-go"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeFetchItem builds a FetchItem closure backed by a map[parentID]struct.
// The struct lets each parent return either (exists, props, native) or an
// error — used by the table-driven test cases below to exercise success,
// NotFound, and propagation paths in one go.
type fakeFetchOutcome struct {
	exists    bool
	props     map[string]any
	nativeIDs map[string]string
	err       error
}

func fakeFetchItem(outcomes map[string]fakeFetchOutcome, calls *atomic.Int64) func(context.Context, aws.Config, string, string) (bool, map[string]any, map[string]string, error) {
	return func(_ context.Context, _ aws.Config, _ string, parentID string) (bool, map[string]any, map[string]string, error) {
		if calls != nil {
			calls.Add(1)
		}
		o, ok := outcomes[parentID]
		if !ok {
			return false, nil, nil, nil
		}
		return o.exists, o.props, o.nativeIDs, o.err
	}
}

// fakeAPIErr builds a smithy.APIError with the given ErrorCode. The
// discoverer doesn't actually inspect smithy errors directly — only the
// per-type FetchItem closures do — but the framework needs to know
// errors propagate unmodified through the gctx-aware ServiceWarn path.
func fakeAPIErr(code, message string) error {
	return &smithy.GenericAPIError{Code: code, Message: message, Fault: smithy.FaultClient}
}

func sdkOnlyTestConfig() sdkOnlySubresourceConfig {
	return sdkOnlySubresourceConfig{
		TFType:               "aws_test_subresource",
		Slug:                 "test_subresource",
		ParentCFNType:        "AWS::Test::Parent",
		SkipProjectTagFilter: true,
		ImportIDFromParent:   func(parentID string, _ map[string]any) string { return parentID },
		NameHintFromParent:   func(parentID string, _ map[string]any) string { return parentID + "-sub" },
	}
}

// TestSDKOnlySubresourceDiscover_HappyPath pins the canonical full read
// path: ListParents -> per-parent FetchItem fan-out -> ImportedResource
// emit with the same identity / tier / source shape as the CC
// discoverer. Each load-bearing field is pinned by exact value (not
// just non-emptiness) so a mutation that swaps ImportID / NameHint /
// Region / AccountID / Tags does not survive.
func TestSDKOnlySubresourceDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	outcomes := map[string]fakeFetchOutcome{
		"bucket-a": {exists: true, props: map[string]any{"Bucket": "bucket-a"}, nativeIDs: map[string]string{"bucket": "bucket-a"}},
		"bucket-b": {exists: true, props: map[string]any{"Bucket": "bucket-b"}, nativeIDs: map[string]string{"bucket": "bucket-b"}},
	}
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return []string{"bucket-a", "bucket-b"}, nil
	}
	cfg.FetchItem = fakeFetchItem(outcomes, nil)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2; events=%+v", len(got), rec.snapshot())
	}
	// Sorted by parentID — deterministic emit order.
	if got[0].Identity.ImportID != "bucket-a" || got[1].Identity.ImportID != "bucket-b" {
		t.Errorf("ImportIDs=[%q,%q], want [bucket-a,bucket-b] (sorted)", got[0].Identity.ImportID, got[1].Identity.ImportID)
	}
	for i, want := range []string{"bucket-a", "bucket-b"} {
		ir := got[i]
		if ir.Identity.Type != "aws_test_subresource" {
			t.Errorf("%s: Type=%q, want aws_test_subresource", want, ir.Identity.Type)
		}
		if ir.Identity.NameHint != want+"-sub" {
			t.Errorf("%s: NameHint=%q, want %s-sub", want, ir.Identity.NameHint, want)
		}
		if ir.Identity.NativeIDs["bucket"] != want {
			t.Errorf("%s: NativeIDs[bucket]=%q, want %s", want, ir.Identity.NativeIDs["bucket"], want)
		}
		if ir.Identity.Region != "us-east-1" {
			t.Errorf("%s: Region=%q, want us-east-1", want, ir.Identity.Region)
		}
		if ir.Identity.AccountID != "123" {
			t.Errorf("%s: AccountID=%q, want 123", want, ir.Identity.AccountID)
		}
		// Untaggable: must be non-nil empty (not nil) — load-bearing
		// per #289 gap-#6 nil-vs-empty contract.
		if ir.Identity.Tags == nil || len(ir.Identity.Tags) != 0 {
			t.Errorf("%s: Tags=%v, want non-nil empty map", want, ir.Identity.Tags)
		}
		if ir.Tier != imported.TierImportedFlat {
			t.Errorf("%s: Tier=%v, want TierImportedFlat", want, ir.Tier)
		}
		if ir.Source != imported.SourceImporter {
			t.Errorf("%s: Source=%v, want SourceImporter", want, ir.Source)
		}
	}
	// Observability: ServiceStart + per-item ItemFound + ServiceFinish.
	wantKinds := []string{"service_start", "item_found", "item_found", "service_finish", "stage_finish"}
	// stage_finish is emitted by the aggregator, not the per-service
	// Discover. Per-service emits omit it; assert only the ones the
	// discoverer owns.
	events := rec.snapshot()
	kinds := make([]string, 0, len(events))
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if len(events) < 4 {
		t.Fatalf("events=%v, want at least ServiceStart + 2 ItemFound + ServiceFinish; got %v", kinds, wantKinds)
	}
	if events[0].Kind != "service_start" || events[0].Service != "test_subresource" || events[0].Region != "us-east-1" {
		t.Errorf("events[0]=%+v, want service_start/test_subresource/us-east-1", events[0])
	}
	last := events[len(events)-1]
	if last.Kind != "service_finish" || last.Count != 2 {
		t.Errorf("last event=%+v, want service_finish with Count=2", last)
	}
}

// TestSDKOnlySubresourceDiscover_EmptyParentsCleanFinish pins the
// no-parents path: ListParents returns [], no FetchItem fan-out fires,
// and ServiceFinish lands with Count=0. A regression that runs the
// errgroup against an empty parent set would still emit ServiceFinish
// but might log a confusing per-item warn — assert the warn count is
// zero too.
func TestSDKOnlySubresourceDiscover_EmptyParentsCleanFinish(t *testing.T) {
	t.Parallel()
	var fetchCalls atomic.Int64
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return nil, nil
	}
	cfg.FetchItem = fakeFetchItem(nil, &fetchCalls)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0 (no parents)", len(got))
	}
	if fetchCalls.Load() != 0 {
		t.Errorf("FetchItem called %d times, want 0", fetchCalls.Load())
	}
	events := rec.snapshot()
	if len(events) != 2 {
		t.Fatalf("events=%d (%+v), want 2 (service_start + service_finish)", len(events), events)
	}
	if events[0].Kind != "service_start" || events[1].Kind != "service_finish" {
		t.Errorf("events=%v, want [service_start, service_finish]", events)
	}
	if events[1].Count != 0 {
		t.Errorf("service_finish Count=%d, want 0", events[1].Count)
	}
	for _, e := range events {
		if e.Kind == "service_warn" {
			t.Errorf("unexpected warn on empty-parents path: %+v", e)
		}
	}
}

// TestSDKOnlySubresourceDiscover_FetchItemNotFoundSkipsParent pins the
// per-item "not configured" semantics: a FetchItem that returns
// (exists=false, nil, nil, nil) — the contract for NoSuchVersioningConfiguration
// / NoSuchLifecycleConfiguration / OwnershipControlsNotFoundError /
// NoSuchPublicAccessBlockConfiguration /
// ServerSideEncryptionConfigurationNotFoundError — does NOT emit a
// warn (it's a normal state, not an error) and does NOT emit an
// ImportedResource for that parent.
func TestSDKOnlySubresourceDiscover_FetchItemNotFoundSkipsParent(t *testing.T) {
	t.Parallel()
	outcomes := map[string]fakeFetchOutcome{
		"bucket-configured":     {exists: true, props: map[string]any{}, nativeIDs: map[string]string{"bucket": "bucket-configured"}},
		"bucket-not-configured": {exists: false},
	}
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return []string{"bucket-configured", "bucket-not-configured"}, nil
	}
	cfg.FetchItem = fakeFetchItem(outcomes, nil)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only the configured bucket emits)", len(got))
	}
	if got[0].Identity.ImportID != "bucket-configured" {
		t.Errorf("ImportID=%q, want bucket-configured", got[0].Identity.ImportID)
	}
	for _, e := range rec.snapshot() {
		if e.Kind == "service_warn" {
			t.Errorf("not-set FetchItem result must not emit warn; got %+v", e)
		}
	}
}

// TestSDKOnlySubresourceDiscover_FetchItemErrorWarnsAndContinues pins
// the per-item soft-fail posture: a FetchItem error emits a ServiceWarn
// and skips that parent, but other parents still get fetched and
// emitted. Matches cloudControlDiscoverer's GetResource posture
// (cloudcontrol_discoverer.go:373-378).
func TestSDKOnlySubresourceDiscover_FetchItemErrorWarnsAndContinues(t *testing.T) {
	t.Parallel()
	outcomes := map[string]fakeFetchOutcome{
		"bucket-ok":  {exists: true, props: map[string]any{}, nativeIDs: map[string]string{"bucket": "bucket-ok"}},
		"bucket-err": {err: fakeAPIErr("AccessDenied", "no permission")},
	}
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return []string{"bucket-ok", "bucket-err"}, nil
	}
	cfg.FetchItem = fakeFetchItem(outcomes, nil)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatalf("soft-fail: per-item err must NOT propagate; got %v", err)
	}
	if len(got) != 1 || got[0].Identity.ImportID != "bucket-ok" {
		t.Errorf("got=%v, want only bucket-ok emitted", got)
	}
	var sawWarn bool
	for _, e := range rec.snapshot() {
		if e.Kind == "service_warn" && strings.Contains(e.Message, "bucket-err") && strings.Contains(e.Message, "AccessDenied") {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Errorf("expected service_warn mentioning bucket-err+AccessDenied; events=%+v", rec.snapshot())
	}
}

// TestSDKOnlySubresourceDiscover_ListParentsErrorAbortsRegion pins
// that an enumeration error short-circuits this region (the discoverer
// can't proceed without a parent list) and propagates a wrapped error
// to the caller. Matches the cloudControlDiscoverer ListResources
// posture (cloudcontrol_discoverer.go:330-332).
func TestSDKOnlySubresourceDiscover_ListParentsErrorAbortsRegion(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("listparents-seed")
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return nil, seedErr
	}
	cfg.FetchItem = fakeFetchItem(nil, nil)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	rec := &recordingEmitter{}
	_, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err=%v, want errors.Is(err, seedErr)", err)
	}
	// ServiceFinish must still emit (count=0) so the observability
	// stream is well-formed even on the abort path.
	events := rec.snapshot()
	if events[len(events)-1].Kind != "service_finish" || events[len(events)-1].Count != 0 {
		t.Errorf("must emit service_finish with Count=0 on abort; got %+v", events[len(events)-1])
	}
}

// TestSDKOnlySubresourceDiscover_MultiRegionLoopsAndIsolatesCount pins
// that args.Regions drives one ServiceStart/Finish pair per region and
// that the per-region item count is local to that region (not the
// cumulative cross-region accumulator).
func TestSDKOnlySubresourceDiscover_MultiRegionLoopsAndIsolatesCount(t *testing.T) {
	t.Parallel()
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, region string, _ DiscoverArgs) ([]string, error) {
		switch region {
		case "us-east-1":
			return []string{"east-a", "east-b"}, nil
		case "eu-west-1":
			return []string{"west-a"}, nil
		}
		return nil, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _ string, parentID string) (bool, map[string]any, map[string]string, error) {
		return true, map[string]any{}, map[string]string{"bucket": parentID}, nil
	}

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (2 east + 1 west)", len(got))
	}
	// Group ServiceFinish events by region; assert per-region Count.
	finishByRegion := map[string]int{}
	for _, e := range rec.snapshot() {
		if e.Kind == "service_finish" {
			finishByRegion[e.Region] = e.Count
		}
	}
	if finishByRegion["us-east-1"] != 2 {
		t.Errorf("us-east-1 ServiceFinish Count=%d, want 2", finishByRegion["us-east-1"])
	}
	if finishByRegion["eu-west-1"] != 1 {
		t.Errorf("eu-west-1 ServiceFinish Count=%d, want 1 (region-local, not cumulative)", finishByRegion["eu-west-1"])
	}
}

// TestSDKOnlySubresourceDiscover_RGTCacheShortCircuitTaggable pins
// the cache-hit path for hypothetical taggable sub-resources whose
// parent is also taggable: when SkipProjectTagFilter is unset and the
// RGT cache has identifiers for ParentCFNType, ListParents must NOT
// run. The 5 14k1 S3 sub-resources all SET SkipProjectTagFilter=true
// so they always run ListParents; this test guards the framework's
// cache-hit path for future taggable consumers.
func TestSDKOnlySubresourceDiscover_RGTCacheShortCircuitTaggable(t *testing.T) {
	t.Parallel()
	var listCalls atomic.Int64
	cfg := sdkOnlyTestConfig()
	cfg.SkipProjectTagFilter = false
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		listCalls.Add(1)
		return []string{"should-not-be-called"}, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _ string, parentID string) (bool, map[string]any, map[string]string, error) {
		return true, map[string]any{}, map[string]string{"bucket": parentID}, nil
	}

	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {
			"AWS::Test::Parent": {
				{ARN: "arn:cached-a", Identifier: "cached-a"},
				{ARN: "arn:cached-b", Identifier: "cached-b"},
			},
		},
	}}
	args := DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	}.withRGTCache(cache)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	got, err := d.Discover(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d, want 2 (from cache)", len(got))
	}
	if listCalls.Load() != 0 {
		t.Errorf("ListParents called %d times, want 0 (cache hit must short-circuit)", listCalls.Load())
	}
}

// TestSDKOnlySubresourceDiscover_RGTCacheBypassedForUntaggable pins
// the SkipProjectTagFilter=true behavior: even when the RGT cache has
// entries for ParentCFNType, the discoverer must run ListParents
// (matches cloudControlDiscoverer's untaggable-type posture). The 5
// 14k1 S3 sub-resources rely on this branch — RGT only sees tagged
// ARNs but the SUB-resource is untaggable, so we always want a fresh
// parent list.
func TestSDKOnlySubresourceDiscover_RGTCacheBypassedForUntaggable(t *testing.T) {
	t.Parallel()
	var listCalls atomic.Int64
	cfg := sdkOnlyTestConfig() // SkipProjectTagFilter=true by default
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		listCalls.Add(1)
		return []string{"fresh-parent"}, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _ string, parentID string) (bool, map[string]any, map[string]string, error) {
		return true, map[string]any{}, map[string]string{"bucket": parentID}, nil
	}

	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {
			"AWS::Test::Parent": {
				{ARN: "arn:should-be-ignored", Identifier: "should-be-ignored"},
			},
		},
	}}
	args := DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	}.withRGTCache(cache)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	got, err := d.Discover(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Identity.ImportID != "fresh-parent" {
		t.Errorf("got=%v, want [fresh-parent] (cache ignored for untaggable)", got)
	}
	if listCalls.Load() != 1 {
		t.Errorf("ListParents called %d times, want 1 (untaggable types always re-enumerate)", listCalls.Load())
	}
}

// TestSDKOnlySubresourceDiscoverByID_HappyPath exercises the dep-chase
// entry point: a single-resource lookup via FetchItem.
func TestSDKOnlySubresourceDiscoverByID_HappyPath(t *testing.T) {
	t.Parallel()
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return nil, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _ string, parentID string) (bool, map[string]any, map[string]string, error) {
		if parentID == "real-bucket" {
			return true, map[string]any{}, map[string]string{"bucket": parentID}, nil
		}
		return false, nil, nil, nil
	}

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	ir, err := d.DiscoverByID(context.Background(), "real-bucket", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if ir.Identity.ImportID != "real-bucket" {
		t.Errorf("ImportID=%q, want real-bucket", ir.Identity.ImportID)
	}
	if ir.Identity.NameHint != "real-bucket-sub" {
		t.Errorf("NameHint=%q, want real-bucket-sub", ir.Identity.NameHint)
	}
}

// TestSDKOnlySubresourceDiscoverByID_EmptyIDReturnsErrNotSupported
// pins the empty-ID-as-not-supported contract: dep-chase iterates
// candidate discoverers and a blank ID for this sub-resource is
// "not parseable here, try another."
func TestSDKOnlySubresourceDiscoverByID_EmptyIDReturnsErrNotSupported(t *testing.T) {
	t.Parallel()
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return nil, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _ string, _ string) (bool, map[string]any, map[string]string, error) {
		return true, nil, nil, nil
	}
	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	_, err := d.DiscoverByID(context.Background(), "   ", "us-east-1", "123")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("err=%v, want ErrNotSupported", err)
	}
}

// TestSDKOnlySubresourceDiscoverByID_NotFoundMapsToErrNotFound pins
// the contract that FetchItem (exists=false, nil, nil, nil) maps to
// ErrNotFound. Stage 2c3's dep-chase converts this to a warning.
func TestSDKOnlySubresourceDiscoverByID_NotFoundMapsToErrNotFound(t *testing.T) {
	t.Parallel()
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return nil, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _ string, _ string) (bool, map[string]any, map[string]string, error) {
		return false, nil, nil, nil
	}
	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	_, err := d.DiscoverByID(context.Background(), "missing-bucket", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// TestSDKOnlySubresourceDiscoverByID_ErrorPropagatesUnwrapped pins
// the dep-chase contract: a real SDK error (not a NotFound) propagates
// up unmodified so the caller can distinguish transient/permanent
// failures from "resource does not exist." Unlike the bulk Discover
// path, DiscoverByID does NOT soft-fail.
func TestSDKOnlySubresourceDiscoverByID_ErrorPropagatesUnwrapped(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("sdk-seed")
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		return nil, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _ string, _ string) (bool, map[string]any, map[string]string, error) {
		return false, nil, nil, seedErr
	}
	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	_, err := d.DiscoverByID(context.Background(), "bucket-x", "us-east-1", "123")
	if !errors.Is(err, seedErr) {
		t.Errorf("err=%v, want errors.Is(err, seedErr) for transient SDK failure", err)
	}
}

// TestNewSDKOnlySubresourceDiscoverer_NonPositiveConcurrencyFallsBack
// pins the safety-net: a non-positive maxConcurrency should fall back
// to DefaultMaxConcurrency rather than serializing (errgroup.SetLimit(0)
// blocks forever).
func TestNewSDKOnlySubresourceDiscoverer_NonPositiveConcurrencyFallsBack(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1, -42} {
		d := newSDKOnlySubresourceDiscoverer(sdkOnlyTestConfig(), aws.Config{}, n)
		if d.maxConcurrency != DefaultMaxConcurrency {
			t.Errorf("n=%d: maxConcurrency=%d, want %d", n, d.maxConcurrency, DefaultMaxConcurrency)
		}
	}
}

// TestSDKOnlySubresourceDiscover_RespectsContextCancellation pins
// that gctx.Err() at the top of each goroutine short-circuits the
// fan-out when the caller cancels. Matches cloudControlDiscoverer's
// per-item cancel posture (cloudcontrol_discoverer.go:364-366).
func TestSDKOnlySubresourceDiscover_RespectsContextCancellation(t *testing.T) {
	t.Parallel()
	var fetchCalls atomic.Int64
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		// Return 100 parents — even if fan-out is serialized at 1
		// goroutine, the goroutine loop should exit on context-cancel.
		out := make([]string, 100)
		for i := range out {
			out[i] = "p"
		}
		return out, nil
	}
	// Block until ctx is cancelled to ensure at least one goroutine
	// witnesses gctx.Err() != nil.
	cfg.FetchItem = func(ctx context.Context, _ aws.Config, _ string, _ string) (bool, map[string]any, map[string]string, error) {
		fetchCalls.Add(1)
		<-ctx.Done()
		return false, nil, nil, ctx.Err()
	}

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	var discoverErr error
	go func() {
		defer wg.Done()
		_, discoverErr = d.Discover(ctx, DiscoverArgs{
			Regions:   []string{"us-east-1"},
			AccountID: "123",
		})
	}()
	cancel()
	wg.Wait()
	if discoverErr == nil {
		t.Fatal("expected discoverErr on cancelled context")
	}
	if !errors.Is(discoverErr, context.Canceled) {
		t.Errorf("err=%v, want errors.Is(err, context.Canceled)", discoverErr)
	}
}

// TestSDKOnlySubresourceDiscover_EnumerateParentsRejectsNilListParents
// surfaces the registration-time-bug-as-runtime-error path: a config
// whose ListParents is nil is itself a bug (the var anchor in
// sdkonly_s3.go enforces non-nil at package init), but the discoverer
// also fails-loud at runtime rather than silently emitting zero so a
// future regression that constructs a config dynamically still trips
// the contract.
func TestSDKOnlySubresourceDiscover_EnumerateParentsRejectsNilListParents(t *testing.T) {
	t.Parallel()
	cfg := sdkOnlyTestConfig()
	cfg.ListParents = nil // intentional regression
	cfg.FetchItem = fakeFetchItem(nil, nil)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	_, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err == nil || !strings.Contains(err.Error(), "ListParents") {
		t.Errorf("err=%v, want error mentioning ListParents", err)
	}
}

// TestSDKOnlySubresourceDiscover_ResourceTypeMatchesConfig pins the
// ResourceType() accessor against cfg.TFType — used by the aggregator's
// per-type dispatch.
func TestSDKOnlySubresourceDiscover_ResourceTypeMatchesConfig(t *testing.T) {
	t.Parallel()
	d := newSDKOnlySubresourceDiscoverer(sdkOnlyTestConfig(), aws.Config{}, DefaultMaxConcurrency)
	if d.ResourceType() != "aws_test_subresource" {
		t.Errorf("ResourceType()=%q, want aws_test_subresource", d.ResourceType())
	}
}
