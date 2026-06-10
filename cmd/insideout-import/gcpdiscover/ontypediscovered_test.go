package gcpdiscover

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Tests for DiscoverArgs.OnTypeDiscovered — the GCP twin of the per-type
// RESULTS callback (reliable#2060). Mirrors the awsdiscover-package tests.
// The GCP path is largely sequential (one bulk CAI search, then per-type
// translation, then a sequential non-CAI lister phase), so the callbacks
// fire in completion order without genuine concurrency; the serialization
// mutex is still asserted -race-clean via an unguarded counter.

// gcpRecordingCB records per-type deliveries with an intentionally
// unguarded map/slice/counter — under -race this proves DiscoverTypes
// serializes invocations (consumers need no locking of their own).
type gcpRecordingCB struct {
	byType map[string][]string
	order  []string
	calls  int
}

func newGCPRecordingCB() *gcpRecordingCB {
	return &gcpRecordingCB{byType: map[string][]string{}}
}

func (r *gcpRecordingCB) fn(tfType string, resources []imported.ImportedResource) {
	addrs := make([]string, 0, len(resources))
	for _, res := range resources {
		addrs = append(addrs, res.Identity.ImportID)
	}
	r.byType[tfType] = addrs
	r.order = append(r.order, tfType)
	r.calls++
}

// TestOnTypeDiscovered_GCP_DeliversEachTypeOnce asserts the headline
// contract across BOTH GCP dispatch paths: CAI-backed types (translated
// from the bulk SearchAllResources) and a non-CAI type (listed separately).
// Each requested type fires exactly once with its resources, including a
// zero-result non-CAI type (empty non-nil slice). Run with -race this also
// proves serialization (the callback mutates an unguarded map/slice/counter).
func TestOnTypeDiscovered_GCP_DeliversEachTypeOnce(t *testing.T) {
	t.Parallel()

	fake := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/zeta", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
		{Name: "//storage.googleapis.com/io-foo-bucket", AssetType: "storage.googleapis.com/Bucket", Project: "real-proj", Location: "us-central1"},
	}}
	// google_logging_project_sink is ScopeStyleNonCAI; with the default
	// (nil) SinkLister its ListNonCAI returns empty — exercises the
	// non-CAI zero-result delivery.
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	cb := newGCPRecordingCB()
	types := []string{"google_pubsub_topic", "google_storage_bucket", "google_logging_project_sink"}
	got, err := g.DiscoverTypes(context.Background(), types, DiscoverArgs{Project: "io-foo", Emitter: &recordingEmitter{}, OnTypeDiscovered: cb.fn})
	if err != nil {
		t.Fatal(err)
	}

	if cb.calls != 3 {
		t.Errorf("callback fired %d times, want 3 (once per requested type)", cb.calls)
	}
	// CAI-backed types deliver their translated rows.
	if got := cb.byType["google_pubsub_topic"]; len(got) != 2 {
		t.Errorf("google_pubsub_topic delivered %v, want 2 rows", got)
	}
	if got := cb.byType["google_storage_bucket"]; len(got) != 1 {
		t.Errorf("google_storage_bucket delivered %v, want 1 row", got)
	}
	// The zero-result non-CAI type still delivered, with an empty non-nil slice.
	sinkRes, ok := cb.byType["google_logging_project_sink"]
	if !ok {
		t.Fatal("non-CAI google_logging_project_sink never delivered to callback")
	}
	if sinkRes == nil || len(sinkRes) != 0 {
		t.Errorf("google_logging_project_sink delivered %v, want [] (empty non-nil)", sinkRes)
	}
	// The flattened return value carries the CAI rows (3 total here).
	if len(got) != 3 {
		t.Errorf("len(got)=%d, want 3", len(got))
	}
}

// TestOnTypeDiscovered_GCP_NonCAIPopulatedLister asserts a non-CAI type
// with a real lister delivers its filtered rows exactly once via the
// callback — covering the non-CAI completion point with populated results.
func TestOnTypeDiscovered_GCP_NonCAIPopulatedLister(t *testing.T) {
	t.Parallel()

	fake := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
	}}
	sinkLister := &fakeLoggingSinkLister{sinks: []gcpLoggingSink{
		{Name: "io-foo-audit-sink", FullName: "projects/real-proj/sinks/io-foo-audit-sink", Destination: "storage.googleapis.com/io-foo-audit"},
		{Name: "io-foo-flow-sink", FullName: "projects/real-proj/sinks/io-foo-flow-sink", Destination: "storage.googleapis.com/io-foo-flow"},
		// Builtin + non-stack sinks are filtered out by ListNonCAI.
		{Name: "_Default", FullName: "projects/real-proj/sinks/_Default"},
		{Name: "other-stack-sink", FullName: "projects/real-proj/sinks/other-stack-sink"},
	}}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{SinkLister: sinkLister})

	cb := newGCPRecordingCB()
	types := []string{"google_pubsub_topic", "google_logging_project_sink"}
	if _, err := g.DiscoverTypes(context.Background(), types, DiscoverArgs{Project: "io-foo", Emitter: &recordingEmitter{}, OnTypeDiscovered: cb.fn}); err != nil {
		t.Fatal(err)
	}

	if cb.calls != 2 {
		t.Errorf("callback fired %d times, want 2 (one per type, no double-emit)", cb.calls)
	}
	if got := cb.byType["google_pubsub_topic"]; len(got) != 1 {
		t.Errorf("google_pubsub_topic delivered %v, want 1 row", got)
	}
	// Only the two stack-scoped sinks survive ListNonCAI's filter.
	if got := cb.byType["google_logging_project_sink"]; len(got) != 2 {
		t.Errorf("google_logging_project_sink delivered %v, want 2 rows", got)
	}
}

// TestOnTypeDiscovered_GCP_NilCallbackIsBackCompat pins back-compat: a nil
// callback runs the unchanged behavior without panic and returns the same
// flattened slice.
func TestOnTypeDiscovered_GCP_NilCallbackIsBackCompat(t *testing.T) {
	t.Parallel()

	fake := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
	}}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, DiscoverArgs{Emitter: &recordingEmitter{}}) // nil callback
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("len(got)=%d, want 1", len(got))
	}
}

// TestOnTypeDiscovered_GCP_DeliveredSetMatchesRequested asserts the union
// of delivered types equals the requested set (no phantom, no missing).
func TestOnTypeDiscovered_GCP_DeliveredSetMatchesRequested(t *testing.T) {
	t.Parallel()

	fake := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
		{Name: "//storage.googleapis.com/io-foo-bucket", AssetType: "storage.googleapis.com/Bucket", Project: "real-proj", Location: "us-central1"},
	}}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	cb := newGCPRecordingCB()
	want := []string{"google_pubsub_topic", "google_storage_bucket", "google_logging_project_sink"}
	if _, err := g.DiscoverTypes(context.Background(), want, DiscoverArgs{Project: "io-foo", Emitter: &recordingEmitter{}, OnTypeDiscovered: cb.fn}); err != nil {
		t.Fatal(err)
	}
	gotTypes := make([]string, 0, len(cb.byType))
	for tfType := range cb.byType {
		gotTypes = append(gotTypes, tfType)
	}
	slices.Sort(gotTypes)
	slices.Sort(want)
	if !reflect.DeepEqual(gotTypes, want) {
		t.Errorf("delivered types = %v, want %v", gotTypes, want)
	}
}
