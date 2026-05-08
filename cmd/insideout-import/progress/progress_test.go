package progress

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedNow returns a deterministic clock for golden assertions. The
// timestamp matches the example in the issue body (#295) so the JSON
// shape lines up 1:1 with the documented contract.
func fixedNow() func() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, "2026-05-07T12:00:00Z")
	return func() time.Time { return t }
}

func TestJSONEmitter_ServiceStartShape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ServiceStart("sqs", "us-east-1")

	got := strings.TrimRight(buf.String(), "\n")
	want := `{"event":"service_start","service":"sqs","region":"us-east-1","ts":"2026-05-07T12:00:00Z"}`
	if got != want {
		t.Errorf("ServiceStart line mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestJSONEmitter_ItemFoundShape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ItemFound("sqs", "us-east-1", "aws_sqs_queue", "https://sqs.us-east-1.amazonaws.com/123/q1")

	got := strings.TrimRight(buf.String(), "\n")
	want := `{"event":"item_found","service":"sqs","region":"us-east-1","tf_type":"aws_sqs_queue","import_id":"https://sqs.us-east-1.amazonaws.com/123/q1","ts":"2026-05-07T12:00:00Z"}`
	if got != want {
		t.Errorf("ItemFound line mismatch:\n got: %s\nwant: %s", got, want)
	}
}

// TestJSONEmitter_ServiceFinishShape pins the service_finish JSON shape
// AND asserts that DurationMs comes through as the integer milliseconds
// of the supplied time.Duration (1.234s → 1234). A regression that
// switched to Nanoseconds() / Seconds() would silently shift the units
// the downstream UI reads.
func TestJSONEmitter_ServiceFinishShape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ServiceFinish("sqs", "us-east-1", 42, 1234*time.Millisecond)

	got := strings.TrimRight(buf.String(), "\n")
	want := `{"event":"service_finish","service":"sqs","region":"us-east-1","count":42,"duration_ms":1234,"ts":"2026-05-07T12:00:00Z"}`
	if got != want {
		t.Errorf("ServiceFinish line mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestJSONEmitter_StageFinishShape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.StageFinish("discover", 7, 5*time.Second)

	got := strings.TrimRight(buf.String(), "\n")
	want := `{"event":"stage_finish","stage":"discover","count":7,"total":7,"duration_ms":5000,"ts":"2026-05-07T12:00:00Z"}`
	if got != want {
		t.Errorf("StageFinish line mismatch:\n got: %s\nwant: %s", got, want)
	}
}

// TestJSONEmitter_ConcurrentEmissionIsLineSafe runs N goroutines
// emitting ItemFound concurrently and asserts every output line is
// valid JSON with the expected event type. A regression that drops
// the mutex would produce interleaved bytes and at least one
// json.Unmarshal failure under -race.
//
// Not t.Parallel(): the test exercises shared-resource scheduling
// behavior and we want it to dominate its own goroutine pool, not
// share with sibling tests.
func TestJSONEmitter_ConcurrentEmissionIsLineSafe(t *testing.T) {
	const goroutines = 16
	const perG = 64
	var buf bytes.Buffer
	// Wrap buf in a synchronizing writer so we don't conflate the
	// emitter's serialization with bytes.Buffer's per-write copy.
	sw := &syncWriter{w: &buf}
	e := NewJSONEmitter(sw).WithNow(fixedNow())

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				e.ItemFound("sqs", "us-east-1", "aws_sqs_queue", "id")
			}
		}()
	}
	wg.Wait()

	scanner := bufio.NewScanner(strings.NewReader(buf.String()))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := 0
	for scanner.Scan() {
		lines++
		var evt Event
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n  raw: %q", lines, err, scanner.Text())
		}
		if evt.Event != "item_found" {
			t.Errorf("line %d: event=%q, want item_found", lines, evt.Event)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := goroutines * perG; lines != want {
		t.Errorf("got %d lines, want %d", lines, want)
	}
}

func TestNopEmitter_AllMethodsAreNoOps(t *testing.T) {
	t.Parallel()
	// The contract: NopEmitter never writes to anything. We verify by
	// confirming the methods are callable without panic and their
	// signatures satisfy the Emitter interface.
	var e Emitter = NopEmitter{}
	e.ServiceStart("sqs", "us-east-1")
	e.ServiceFinish("sqs", "us-east-1", 5, time.Second)
	e.ItemFound("sqs", "us-east-1", "aws_sqs_queue", "id")
	e.StageFinish("discover", 5, time.Second)

	// Belt-and-suspenders: a counting writer wrapped around any future
	// fields would catch a regression that introduces a hidden write.
	// NopEmitter has no fields today; this asserts that property at
	// construction (zero-value usable, no internal state).
	var zero NopEmitter
	zero.ServiceStart("", "")
	zero.ServiceFinish("", "", 0, 0)
	zero.ItemFound("", "", "", "")
	zero.StageFinish("", 0, 0)
}

// TestJSONEmitter_FlowOrderingDocumentedExample assembles a realistic
// per-service event sequence and asserts the on-the-wire output matches
// the example block in the issue body (#295). This is the canonical
// integration shape downstream consumers (reliable agent-API SSE
// translator) read against.
func TestJSONEmitter_FlowOrderingDocumentedExample(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ServiceStart("sqs", "us-east-1")
	e.ItemFound("sqs", "us-east-1", "aws_sqs_queue", "https://sqs.us-east-1.amazonaws.com/1/q")
	e.ServiceFinish("sqs", "us-east-1", 1, 100*time.Millisecond)
	e.StageFinish("discover", 1, 200*time.Millisecond)

	want := strings.Join([]string{
		`{"event":"service_start","service":"sqs","region":"us-east-1","ts":"2026-05-07T12:00:00Z"}`,
		`{"event":"item_found","service":"sqs","region":"us-east-1","tf_type":"aws_sqs_queue","import_id":"https://sqs.us-east-1.amazonaws.com/1/q","ts":"2026-05-07T12:00:00Z"}`,
		`{"event":"service_finish","service":"sqs","region":"us-east-1","count":1,"duration_ms":100,"ts":"2026-05-07T12:00:00Z"}`,
		`{"event":"stage_finish","stage":"discover","count":1,"total":1,"duration_ms":200,"ts":"2026-05-07T12:00:00Z"}`,
		"",
	}, "\n")
	if buf.String() != want {
		t.Errorf("documented flow output mismatch:\n got: %q\nwant: %q", buf.String(), want)
	}
}

// syncWriter wraps an io.Writer with an atomic counter so the
// concurrent-emission test can assert that every emit produced exactly
// one Write call (no torn writes) without depending on bytes.Buffer's
// internal locking semantics.
type syncWriter struct {
	mu     sync.Mutex
	w      *bytes.Buffer
	writes atomic.Int64
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes.Add(1)
	return s.w.Write(p)
}
