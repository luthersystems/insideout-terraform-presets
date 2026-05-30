package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
)

// typeProgressRecorder is a recordingEmitter that ALSO implements
// progress.TypeProgressEmitter, so it receives the per-type completion
// events DiscoverTypes / EnrichAttributes fire for #699. Existing tests
// use the bare recordingEmitter (which does NOT implement the extension)
// — that they stay green proves the non-TypeProgressEmitter path is
// byte-for-byte unchanged.
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

// byType returns the recorded per-type events keyed by TFType. The AWS
// discover walk is concurrent, so callers index by type rather than
// relying on slice order. NOTE: this map collapses any duplicate emit
// for a type — double-emit regressions are caught by the separate
// len(typeSnapshot()) assertions in each test, not here.
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

// irType is like ir() but lets the test pin the Terraform type so the
// enrich test can group by type.
func irType(tfType, addr string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: tfType, Address: addr, ImportID: addr},
		Tier:     imported.TierImportedFlat,
		Source:   imported.SourceImporter,
	}
}

// TestDiscoverTypes_EmitsPerTypeProgress pins the #699 discover-phase
// contract: one TypeDone per selected type, carrying the type's
// found-count and a stable Total == len(selected). A zero-result type
// (type_c) must still emit (Found:0) so the N-of-total denominator
// reaches the total.
func TestDiscoverTypes_EmitsPerTypeProgress(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1"), ir("a2")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	c := &fakeDiscoverer{t: "type_c"} // zero results
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b, "type_c": c}}

	rec := newTypeProgressRecorder()
	args := argsBasic()
	args.Emitter = rec
	if _, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "type_b", "type_c"}, args); err != nil {
		t.Fatal(err)
	}

	events := rec.typeSnapshot()
	if len(events) != 3 {
		t.Fatalf("got %d per-type events, want 3 (one per selected type)", len(events))
	}
	byType := rec.byType()
	for _, tc := range []struct {
		tfType    string
		wantFound int
	}{
		{"type_a", 2},
		{"type_b", 1},
		{"type_c", 0},
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
// an Emitter that does NOT implement TypeProgressEmitter (the bare
// recordingEmitter) runs DiscoverTypes without panicking and produces
// the same result — the per-type path is simply skipped.
func TestDiscoverTypes_PlainEmitterSkipsPerTypePath(t *testing.T) {
	t.Parallel()
	// Guard the design assumption the back-compat path relies on.
	if _, ok := progress.Emitter(&recordingEmitter{}).(progress.TypeProgressEmitter); ok {
		t.Fatal("recordingEmitter unexpectedly implements TypeProgressEmitter; this test no longer proves the plain-emitter path")
	}
	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a}}
	args := argsBasic()
	args.Emitter = &recordingEmitter{}
	got, err := agg.DiscoverTypes(context.Background(), []string{"type_a"}, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("len(got)=%d, want 1", len(got))
	}
}

// TestDiscoverTypes_PerTypeProgress_TimeoutCountsType pins that a
// per-type-timeout partial (#1787) still counts toward the #699
// progress denominator: the timed-out type emits a TypeDone with
// Found:0 so the consumer's "N of total" reaches total even when a type
// is fenced off.
func TestDiscoverTypes_PerTypeProgress_TimeoutCountsType(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skip in -short")
	}
	t.Parallel()
	slow := &sleepDiscoverer{t: "type_slow", out: []imported.ImportedResource{ir("slow1")}, sleep: 500 * time.Millisecond}
	fast := &fakeDiscoverer{t: "type_fast", out: []imported.ImportedResource{ir("fast1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_slow": slow, "type_fast": fast}}

	rec := newTypeProgressRecorder()
	args := argsBasic()
	args.Emitter = rec
	args.PerTypeTimeout = 50 * time.Millisecond
	if _, err := agg.DiscoverTypes(context.Background(), []string{"type_slow", "type_fast"}, args); err != nil {
		t.Fatalf("DiscoverTypes returned error on per-type timeout: %v", err)
	}

	byType := rec.byType()
	if len(byType) != 2 {
		t.Fatalf("got %d distinct per-type events, want 2 (both types complete)", len(byType))
	}
	if got := byType["type_slow"]; got.Found != 0 || got.Total != 2 {
		t.Errorf("type_slow event = %+v, want Found:0 Total:2 (timed-out type still counts)", got)
	}
	if got := byType["type_fast"]; got.Found != 1 || got.Total != 2 {
		t.Errorf("type_fast event = %+v, want Found:1 Total:2", got)
	}
}

// TestEnrichAttributes_EmitsPerTypeProgress pins the #699 enrich-phase
// contract: one TypeDone per enriched type with Phase="enrich",
// Found == resources of that type covered, and Total == distinct
// enrichable types. idx is sorted (type, address), so the serial
// second pass emits in sorted type order — asserted here.
func TestEnrichAttributes_EmitsPerTypeProgress(t *testing.T) {
	t.Parallel()
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		"aws_dynamodb_table": &fakeEnricher{tfType: "aws_dynamodb_table"},
		"aws_s3_bucket":      &fakeEnricher{tfType: "aws_s3_bucket"},
	}}
	irs := []imported.ImportedResource{
		irType("aws_s3_bucket", "b1"),
		irType("aws_dynamodb_table", "t2"),
		irType("aws_dynamodb_table", "t1"),
		// A type with no registered enricher must NOT count toward Total
		// or emit an event.
		irType("aws_sqs_queue", "q1"),
	}
	rec := newTypeProgressRecorder()
	if err := a.EnrichAttributes(context.Background(), irs, EnrichClients{}, rec); err != nil {
		t.Fatal(err)
	}

	events := rec.typeSnapshot()
	if len(events) != 2 {
		t.Fatalf("got %d per-type enrich events, want 2 (one per enrichable type)", len(events))
	}
	// Sorted (type, address): aws_dynamodb_table (2) precedes
	// aws_s3_bucket (1).
	want := []progress.TypeProgress{
		{Phase: "enrich", TFType: "aws_dynamodb_table", Found: 2, Total: 2},
		{Phase: "enrich", TFType: "aws_s3_bucket", Found: 1, Total: 2},
	}
	for i, w := range want {
		if events[i] != w {
			t.Errorf("enrich event[%d] = %+v, want %+v", i, events[i], w)
		}
	}
}

// TestEnrichAttributes_PerTypeProgress_CountsDespiteFailures pins the
// documented enrich-phase determinism contract (enrich.go: "Found counts
// the resources of the type this pass covered ... independent of
// per-resource enrich success"): a type with one failing resource still
// reports Found == the full type count, AND the TypeDone event is still
// emitted even though EnrichAttributes returns a joined error.
func TestEnrichAttributes_PerTypeProgress_CountsDespiteFailures(t *testing.T) {
	t.Parallel()
	// No shrinkEnrichRetryDelays needed: a generic error is not a
	// throttle error, so enrichWithRetry returns immediately without
	// backoff. (shrink mutates package globals and would race with the
	// other parallel enrich tests under -race.)
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		"aws_dynamodb_table": &fakeEnricher{tfType: "aws_dynamodb_table", result: func(ir *imported.ImportedResource) error {
			if ir.Identity.ImportID == "t-bad" {
				return errors.New("boom") // generic error → accumulated → joined return
			}
			ir.Attrs = json.RawMessage(`{}`)
			return nil
		}},
	}}
	irs := []imported.ImportedResource{
		irType("aws_dynamodb_table", "t-bad"),
		irType("aws_dynamodb_table", "t-good"),
	}
	rec := newTypeProgressRecorder()
	if err := a.EnrichAttributes(context.Background(), irs, EnrichClients{}, rec); err == nil {
		t.Fatal("expected a joined error from the failing resource")
	}
	events := rec.typeSnapshot()
	if len(events) != 1 {
		t.Fatalf("got %d per-type events, want 1 (TypeDone must fire even when EnrichAttributes returns an error)", len(events))
	}
	want := progress.TypeProgress{Phase: "enrich", TFType: "aws_dynamodb_table", Found: 2, Total: 1}
	if events[0] != want {
		t.Errorf("event = %+v, want %+v (Found counts all resources of the type regardless of per-resource success)", events[0], want)
	}
}

// TestEnrichAttributes_PerTypeProgress_MidListFailureResetsCount hardens
// the curType/curFound transition logic: with two enrichable types where
// the NON-final type carries a failing resource, the transition emit must
// carry the non-final type's full count (independent of the failure) AND
// the counter must reset to 0 so the final type's count doesn't leak the
// previous type's tail.
func TestEnrichAttributes_PerTypeProgress_MidListFailureResetsCount(t *testing.T) {
	t.Parallel()
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		// Sorts first (non-final): 2 resources, one fails.
		"aws_dynamodb_table": &fakeEnricher{tfType: "aws_dynamodb_table", result: func(ir *imported.ImportedResource) error {
			if ir.Identity.ImportID == "t-bad" {
				return errors.New("boom")
			}
			ir.Attrs = json.RawMessage(`{}`)
			return nil
		}},
		// Sorts last (final): 1 resource, succeeds.
		"aws_s3_bucket": &fakeEnricher{tfType: "aws_s3_bucket"},
	}}
	irs := []imported.ImportedResource{
		irType("aws_dynamodb_table", "t-bad"),
		irType("aws_dynamodb_table", "t-good"),
		irType("aws_s3_bucket", "b1"),
	}
	rec := newTypeProgressRecorder()
	if err := a.EnrichAttributes(context.Background(), irs, EnrichClients{}, rec); err == nil {
		t.Fatal("expected a joined error from the failing resource")
	}
	events := rec.typeSnapshot()
	want := []progress.TypeProgress{
		{Phase: "enrich", TFType: "aws_dynamodb_table", Found: 2, Total: 2}, // full count despite the mid-list failure
		{Phase: "enrich", TFType: "aws_s3_bucket", Found: 1, Total: 2},      // counter reset — not 3
	}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, w := range want {
		if events[i] != w {
			t.Errorf("event[%d] = %+v, want %+v", i, events[i], w)
		}
	}
}

// TestDiscoverTypes_RealBridgeDeliversDiscoverProgress is the
// orchestrator↔bridge integration test: it wires the REAL facade bridge
// (imp.NewProgressEmitter) into a REAL DiscoverTypes run and asserts the
// caller's DiscoverProgress sink receives correctly-translated events
// with a monotonic CompletedTypes counter — the seam the provider-level
// tests can't reach (AWSDiscoverer.byType is package-internal, so a
// fake-backed Provider can't be built from the aws_test package). The
// only hop left uncovered is the Provider's 6-line delegation that sets
// Emitter: imp.NewProgressEmitter(opts.Progress), which is compile-
// checked.
func TestDiscoverTypes_RealBridgeDeliversDiscoverProgress(t *testing.T) {
	t.Parallel()
	a := &fakeDiscoverer{t: "type_a", out: []imported.ImportedResource{ir("a1"), ir("a2")}}
	b := &fakeDiscoverer{t: "type_b", out: []imported.ImportedResource{ir("b1")}}
	agg := &AWSDiscoverer{byType: map[string]Discoverer{"type_a": a, "type_b": b}}

	var mu sync.Mutex
	var got []imp.DiscoverProgress
	sink := func(p imp.DiscoverProgress) {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
	}
	args := argsBasic()
	args.Emitter = imp.NewProgressEmitter(sink) // the real facade bridge
	if _, err := agg.DiscoverTypes(context.Background(), []string{"type_a", "type_b"}, args); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("sink received %d DiscoverProgress events, want 2", len(got))
	}
	// CompletedTypes must cover 1..2 exactly once (bridge-owned counter),
	// and each event carries the translated facade-shape fields.
	completed := map[int]bool{}
	foundByType := map[string]int{}
	for _, p := range got {
		if p.Phase != "discover" {
			t.Errorf("Phase=%q, want discover", p.Phase)
		}
		if p.TotalTypes != 2 {
			t.Errorf("TotalTypes=%d, want 2", p.TotalTypes)
		}
		completed[p.CompletedTypes] = true
		foundByType[p.Type] = p.FoundCount
	}
	if !completed[1] || !completed[2] {
		t.Errorf("CompletedTypes did not cover 1..2: %+v", got)
	}
	if foundByType["type_a"] != 2 || foundByType["type_b"] != 1 {
		t.Errorf("FoundCount by type = %v, want type_a:2 type_b:1", foundByType)
	}
}

// TestEnrichAttributes_NoEnrichableTypes_EmitsNoProgress pins the
// len(idx)==0 guard: when no resource has a registered enricher, the
// serial second pass never runs, so no TypeDone fires — in particular
// no spurious trailing {Phase:"enrich", TFType:"", Found:0} event.
func TestEnrichAttributes_NoEnrichableTypes_EmitsNoProgress(t *testing.T) {
	t.Parallel()
	a := &AWSDiscoverer{byTypeEnricher: map[string]AttributeEnricher{
		"aws_dynamodb_table": &fakeEnricher{tfType: "aws_dynamodb_table"},
	}}
	// Only an unregistered type → idx is empty.
	irs := []imported.ImportedResource{irType("aws_sqs_queue", "q1")}
	rec := newTypeProgressRecorder()
	if err := a.EnrichAttributes(context.Background(), irs, EnrichClients{}, rec); err != nil {
		t.Fatal(err)
	}
	if events := rec.typeSnapshot(); len(events) != 0 {
		t.Errorf("got %d per-type events, want 0 (no enrichable types → no TypeDone, no spurious empty-type event): %+v", len(events), events)
	}
}
