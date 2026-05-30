package imported_test

import (
	"sync"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
)

// TestNewProgressEmitter_NilSinkIsNop pins the back-compat path: a nil
// sink resolves to progress.NopEmitter{}, which (a) is the zero-overhead
// default and (b) does NOT implement TypeProgressEmitter — so the
// orchestrators' per-type emission path is skipped entirely, byte-for-
// byte the pre-#699 behavior.
func TestNewProgressEmitter_NilSinkIsNop(t *testing.T) {
	t.Parallel()
	e := imp.NewProgressEmitter(nil)
	if e != (progress.NopEmitter{}) {
		t.Errorf("NewProgressEmitter(nil) = %#v, want progress.NopEmitter{}", e)
	}
	if _, ok := e.(progress.TypeProgressEmitter); ok {
		t.Error("nil-sink emitter must NOT implement TypeProgressEmitter (would re-enable per-type emission under the default config)")
	}
}

// TestNewProgressEmitter_BridgeTranslatesAndCounts pins the field
// mapping (progress.TypeProgress → imp.DiscoverProgress) and that the
// bridge owns the monotonic CompletedTypes counter: successive TypeDone
// calls deliver CompletedTypes 1, 2, 3 while Phase/Type/FoundCount/
// TotalTypes pass through verbatim.
func TestNewProgressEmitter_BridgeTranslatesAndCounts(t *testing.T) {
	t.Parallel()
	var got []imp.DiscoverProgress
	e := imp.NewProgressEmitter(func(p imp.DiscoverProgress) { got = append(got, p) })

	tp, ok := e.(progress.TypeProgressEmitter)
	if !ok {
		t.Fatal("non-nil-sink emitter must implement TypeProgressEmitter")
	}
	tp.TypeDone(progress.TypeProgress{Phase: "discover", TFType: "aws_s3_bucket", Found: 3, Total: 3})
	tp.TypeDone(progress.TypeProgress{Phase: "discover", TFType: "aws_sqs_queue", Found: 0, Total: 3})
	tp.TypeDone(progress.TypeProgress{Phase: "enrich", TFType: "aws_iam_role", Found: 5, Total: 3})

	want := []imp.DiscoverProgress{
		{Phase: "discover", Type: "aws_s3_bucket", FoundCount: 3, CompletedTypes: 1, TotalTypes: 3},
		{Phase: "discover", Type: "aws_sqs_queue", FoundCount: 0, CompletedTypes: 2, TotalTypes: 3},
		{Phase: "enrich", Type: "aws_iam_role", FoundCount: 5, CompletedTypes: 3, TotalTypes: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestNewProgressEmitter_ServiceEventsAreNoops confirms the bridge
// swallows the per-(service,region) Emitter events — only per-type
// completion reaches the facade sink.
func TestNewProgressEmitter_ServiceEventsAreNoops(t *testing.T) {
	t.Parallel()
	calls := 0
	e := imp.NewProgressEmitter(func(imp.DiscoverProgress) { calls++ })
	e.ServiceStart("svc", "r")
	e.ServiceFinish("svc", "r", 9, 0)
	e.ItemFound("svc", "r", "aws_s3_bucket", "id")
	e.StageFinish("discover", 9, 0)
	e.ServiceWarn("svc", "r", "warn")
	if calls != 0 {
		t.Errorf("service-level events invoked the sink %d times, want 0", calls)
	}
}

// TestProgressBridge_ConcurrentTypeDoneIsSafe is the #699 concurrency
// contract: the AWS discover walk fans out per-type goroutines, so
// TypeDone may be called concurrently. The bridge must serialize the
// sink and hand out each CompletedTypes value exactly once across
// 1..N. Run under -race to catch unsynchronized access.
func TestProgressBridge_ConcurrentTypeDoneIsSafe(t *testing.T) {
	t.Parallel()
	const n = 64

	var mu sync.Mutex
	seen := make([]int, 0, n)
	e := imp.NewProgressEmitter(func(p imp.DiscoverProgress) {
		mu.Lock()
		seen = append(seen, p.CompletedTypes)
		mu.Unlock()
	})
	tp := e.(progress.TypeProgressEmitter)

	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			tp.TypeDone(progress.TypeProgress{Phase: "discover", TFType: "t", Found: 1, Total: n})
		})
	}
	wg.Wait()

	if len(seen) != n {
		t.Fatalf("sink invoked %d times, want %d", len(seen), n)
	}
	// CompletedTypes must cover exactly 1..n, each once — proving the
	// counter increments under the lock without lost updates.
	counts := make(map[int]int, n)
	for _, c := range seen {
		counts[c]++
	}
	for i := 1; i <= n; i++ {
		if counts[i] != 1 {
			t.Errorf("CompletedTypes=%d appeared %d times, want exactly 1 (1..%d each once)", i, counts[i], n)
		}
	}
}
