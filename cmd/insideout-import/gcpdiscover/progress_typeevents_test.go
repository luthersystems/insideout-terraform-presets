package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
)

// gcpIR builds a minimal ImportedResource for the enrich progress tests.
func gcpIR(tfType, addr string) imported.ImportedResource {
	return imported.ImportedResource{Identity: imported.ResourceIdentity{Cloud: "gcp", Type: tfType, Address: addr, ImportID: addr}}
}

// typeProgressRecorder is a recordingEmitter that ALSO implements
// progress.TypeProgressEmitter, capturing the per-type completion events
// DiscoverTypes / EnrichAttributes fire for #699. Mirrors the
// awsdiscover-package helper. Existing GCP tests use the bare
// recordingEmitter (which does NOT implement the extension), so their
// staying green proves the non-TypeProgressEmitter path is unchanged.
type typeProgressRecorder struct {
	*recordingEmitter
	mu         sync.Mutex
	typeEvents []progress.TypeProgress
}

func newTypeProgressRecorder() *typeProgressRecorder {
	return &typeProgressRecorder{recordingEmitter: &recordingEmitter{}}
}

func (r *typeProgressRecorder) TypeDone(p progress.TypeProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.typeEvents = append(r.typeEvents, p)
}

// byType returns the recorded per-type events keyed by TFType. NOTE:
// this map collapses any duplicate emit for a type — double-emit
// regressions are caught by the separate len(typeSnapshot()) assertions
// in each test, not here.
func (r *typeProgressRecorder) byType() map[string]progress.TypeProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]progress.TypeProgress, len(r.typeEvents))
	for _, e := range r.typeEvents {
		out[e.TFType] = e
	}
	return out
}

func (r *typeProgressRecorder) typeSnapshot() []progress.TypeProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]progress.TypeProgress, len(r.typeEvents))
	copy(out, r.typeEvents)
	return out
}

// TestDiscoverTypes_EmitsPerTypeProgress pins the #699 discover-phase
// contract for GCP across BOTH dispatch paths: the CAI-backed types
// (translated from one bulk SearchAllResources) and a non-CAI type
// (listed separately). Each selected type emits exactly one TypeDone
// with Phase="discover" and a stable Total == len(selected); the
// non-CAI type with no lister still emits (Found:0) so the denominator
// reaches the total.
func TestDiscoverTypes_EmitsPerTypeProgress(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/zeta", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
			{Name: "//storage.googleapis.com/io-foo-bucket", AssetType: "storage.googleapis.com/Bucket", Project: "real-proj", Location: "us-central1"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})
	rec := newTypeProgressRecorder()
	// google_logging_project_sink is ScopeStyleNonCAI; with the default
	// (nil) SinkLister its ListNonCAI returns empty, exercising the
	// non-CAI emission branch with Found:0.
	types := []string{"google_pubsub_topic", "google_storage_bucket", "google_logging_project_sink"}
	if _, err := g.DiscoverTypes(context.Background(), types, DiscoverArgs{Project: "io-foo", Emitter: rec}); err != nil {
		t.Fatal(err)
	}

	events := rec.typeSnapshot()
	if len(events) != 3 {
		t.Fatalf("got %d per-type events, want 3 (one per selected type): %+v", len(events), events)
	}
	byType := rec.byType()
	for _, tc := range []struct {
		tfType    string
		wantFound int
	}{
		{"google_pubsub_topic", 2},
		{"google_storage_bucket", 1},
		{"google_logging_project_sink", 0},
	} {
		got, ok := byType[tc.tfType]
		if !ok {
			t.Errorf("no TypeDone event for %s", tc.tfType)
			continue
		}
		if got.Phase != "discover" {
			t.Errorf("%s: Phase=%q, want discover", tc.tfType, got.Phase)
		}
		if got.Found != tc.wantFound {
			t.Errorf("%s: Found=%d, want %d", tc.tfType, got.Found, tc.wantFound)
		}
		if got.Total != 3 {
			t.Errorf("%s: Total=%d, want 3", tc.tfType, got.Total)
		}
	}
}

// TestDiscoverTypes_PlainEmitterSkipsPerTypePath confirms back-compat:
// the bare recordingEmitter (not a TypeProgressEmitter) runs
// DiscoverTypes without panicking and produces results — the per-type
// path is simply skipped.
func TestDiscoverTypes_PlainEmitterSkipsPerTypePath(t *testing.T) {
	t.Parallel()
	if _, ok := progress.Emitter(&recordingEmitter{}).(progress.TypeProgressEmitter); ok {
		t.Fatal("recordingEmitter unexpectedly implements TypeProgressEmitter; this test no longer proves the plain-emitter path")
	}
	fake := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
	}}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, DiscoverArgs{Emitter: &recordingEmitter{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("len(got)=%d, want 1", len(got))
	}
}

// TestEnrichAttributes_EmitsPerTypeProgress pins the #699 enrich-phase
// contract for GCP: one TypeDone per enriched type, Phase="enrich",
// Found == resources of that type covered, Total == distinct enrichable
// types, in sorted type order (idx is sorted by (type, address)).
func TestEnrichAttributes_EmitsPerTypeProgress(t *testing.T) {
	t.Parallel()
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		"google_pubsub_topic":   &fakeEnricher{tfType: "google_pubsub_topic"},
		"google_storage_bucket": &fakeEnricher{tfType: "google_storage_bucket"},
	}}
	irs := []imported.ImportedResource{
		gcpIR("google_storage_bucket", "b2"),
		gcpIR("google_storage_bucket", "b1"),
		gcpIR("google_pubsub_topic", "t1"),
		// No enricher registered → excluded from Total and emits nothing.
		gcpIR("google_compute_network", "n1"),
	}
	rec := newTypeProgressRecorder()
	if err := g.EnrichAttributes(context.Background(), irs, EnrichClients{}, rec); err != nil {
		t.Fatal(err)
	}

	events := rec.typeSnapshot()
	if len(events) != 2 {
		t.Fatalf("got %d per-type enrich events, want 2 (one per enrichable type): %+v", len(events), events)
	}
	// Sorted (type, address): google_pubsub_topic (1) precedes
	// google_storage_bucket (2).
	want := []progress.TypeProgress{
		{Phase: "enrich", TFType: "google_pubsub_topic", Found: 1, Total: 2},
		{Phase: "enrich", TFType: "google_storage_bucket", Found: 2, Total: 2},
	}
	for i, w := range want {
		if events[i] != w {
			t.Errorf("enrich event[%d] = %+v, want %+v", i, events[i], w)
		}
	}
}

// TestDiscoverTypes_NonCAIPerTypeProgress_PopulatedLister exercises the
// non-CAI per-type accumulator (gcpdiscover.go: typeFound++ in the
// non-CAI phase) with a real lister returning multiple rows — the
// earlier discover test only covered the Found:0 nil-lister case. It
// also confirms the non-CAI type emits exactly once (no double-emit
// across the CAI-translation loop and the non-CAI phase).
func TestDiscoverTypes_NonCAIPerTypeProgress_PopulatedLister(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
	}}
	sinkLister := &fakeLoggingSinkLister{sinks: []gcpLoggingSink{
		{Name: "io-foo-audit-sink", FullName: "projects/real-proj/sinks/io-foo-audit-sink", Destination: "storage.googleapis.com/io-foo-audit"},
		{Name: "io-foo-flow-sink", FullName: "projects/real-proj/sinks/io-foo-flow-sink", Destination: "storage.googleapis.com/io-foo-flow"},
		// Builtin + non-stack sinks are filtered out by ListNonCAI, so
		// they must NOT inflate Found.
		{Name: "_Default", FullName: "projects/real-proj/sinks/_Default"},
		{Name: "other-stack-sink", FullName: "projects/real-proj/sinks/other-stack-sink"},
	}}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{SinkLister: sinkLister})
	rec := newTypeProgressRecorder()
	types := []string{"google_pubsub_topic", "google_logging_project_sink"}
	if _, err := g.DiscoverTypes(context.Background(), types, DiscoverArgs{Project: "io-foo", Emitter: rec}); err != nil {
		t.Fatal(err)
	}

	events := rec.typeSnapshot()
	if len(events) != 2 {
		t.Fatalf("got %d per-type events, want 2 (one per selected type, no double-emit): %+v", len(events), events)
	}
	byType := rec.byType()
	if got := byType["google_pubsub_topic"]; got.Found != 1 || got.Total != 2 || got.Phase != "discover" {
		t.Errorf("google_pubsub_topic event = %+v, want {discover,_,1,2}", got)
	}
	// Two stack-scoped sinks survive ListNonCAI's builtin + name-prefix
	// filter; Found must reflect only those.
	if got := byType["google_logging_project_sink"]; got.Found != 2 || got.Total != 2 || got.Phase != "discover" {
		t.Errorf("google_logging_project_sink event = %+v, want {discover,_,2,2}", got)
	}
}

// TestEnrichAttributes_PerTypeProgress_CountsDespiteFailures pins the
// GCP enrich determinism contract: Found counts all resources of a type
// regardless of per-resource enrich success, and TypeDone fires even
// when EnrichAttributes returns a joined error.
func TestEnrichAttributes_PerTypeProgress_CountsDespiteFailures(t *testing.T) {
	t.Parallel()
	// No shrinkEnrichRetryDelays needed: a generic error is not a
	// throttle error, so enrichWithRetry returns immediately without
	// backoff. (shrink mutates package globals and would race with the
	// other parallel enrich tests under -race.)
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		"google_storage_bucket": &fakeEnricher{tfType: "google_storage_bucket", result: func(ir *imported.ImportedResource) error {
			if ir.Identity.ImportID == "b-bad" {
				return errors.New("boom")
			}
			ir.Attrs = json.RawMessage(`{}`)
			return nil
		}},
	}}
	irs := []imported.ImportedResource{
		gcpIR("google_storage_bucket", "b-bad"),
		gcpIR("google_storage_bucket", "b-good"),
	}
	rec := newTypeProgressRecorder()
	if err := g.EnrichAttributes(context.Background(), irs, EnrichClients{}, rec); err == nil {
		t.Fatal("expected a joined error from the failing resource")
	}
	events := rec.typeSnapshot()
	if len(events) != 1 {
		t.Fatalf("got %d per-type events, want 1 (TypeDone must fire even when EnrichAttributes returns an error)", len(events))
	}
	want := progress.TypeProgress{Phase: "enrich", TFType: "google_storage_bucket", Found: 2, Total: 1}
	if events[0] != want {
		t.Errorf("event = %+v, want %+v (Found counts all resources of the type regardless of per-resource success)", events[0], want)
	}
}

// TestEnrichAttributes_NoEnrichableTypes_EmitsNoProgress pins the
// len(idx)==0 guard for GCP: no enrichable resources → no TypeDone, no
// spurious empty-type event.
func TestEnrichAttributes_NoEnrichableTypes_EmitsNoProgress(t *testing.T) {
	t.Parallel()
	g := &GCPDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		"google_storage_bucket": &fakeEnricher{tfType: "google_storage_bucket"},
	}}
	irs := []imported.ImportedResource{gcpIR("google_compute_network", "n1")} // no enricher
	rec := newTypeProgressRecorder()
	if err := g.EnrichAttributes(context.Background(), irs, EnrichClients{}, rec); err != nil {
		t.Fatal(err)
	}
	if events := rec.typeSnapshot(); len(events) != 0 {
		t.Errorf("got %d per-type events, want 0 (no enrichable types → no TypeDone): %+v", len(events), events)
	}
}

// TestDiscoverTypes_RealBridgeDeliversDiscoverProgress is the
// orchestrator↔bridge integration test: it wires the REAL facade bridge
// (imp.NewProgressEmitter) into a REAL DiscoverTypes run and asserts the
// caller's DiscoverProgress sink receives correctly-translated events
// with a monotonic CompletedTypes counter. GCP discovers sequentially,
// so CompletedTypes arrives strictly in order. This covers the seam the
// gcp_test provider tests can't reach (gcpAssetSearcher is package-
// internal); only the Provider's compile-checked delegation remains.
func TestDiscoverTypes_RealBridgeDeliversDiscoverProgress(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
		{Name: "//pubsub.googleapis.com/projects/real-proj/topics/zeta", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
		{Name: "//storage.googleapis.com/io-foo-bucket", AssetType: "storage.googleapis.com/Bucket", Project: "real-proj", Location: "us-central1"},
	}}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	var got []imp.DiscoverProgress
	sink := func(p imp.DiscoverProgress) { got = append(got, p) }
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_storage_bucket"}, DiscoverArgs{
		Project: "io-foo",
		Emitter: imp.NewProgressEmitter(sink), // the real facade bridge
	}); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("sink received %d DiscoverProgress events, want 2: %+v", len(got), got)
	}
	// Sequential discovery → CompletedTypes is strictly 1 then 2.
	for i, p := range got {
		if p.Phase != "discover" {
			t.Errorf("event[%d].Phase=%q, want discover", i, p.Phase)
		}
		if p.TotalTypes != 2 {
			t.Errorf("event[%d].TotalTypes=%d, want 2", i, p.TotalTypes)
		}
		if p.CompletedTypes != i+1 {
			t.Errorf("event[%d].CompletedTypes=%d, want %d (monotonic)", i, p.CompletedTypes, i+1)
		}
	}
	foundByType := map[string]int{}
	for _, p := range got {
		foundByType[p.Type] = p.FoundCount
	}
	if foundByType["google_pubsub_topic"] != 2 || foundByType["google_storage_bucket"] != 1 {
		t.Errorf("FoundCount by type = %v, want google_pubsub_topic:2 google_storage_bucket:1", foundByType)
	}
}
