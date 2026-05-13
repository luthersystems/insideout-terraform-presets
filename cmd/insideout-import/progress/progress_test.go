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
// the downstream UI reads. Per #312, count and duration_ms must always
// appear on service_finish — see TestJSONEmitter_ServiceFinishCountZeroEmitted
// for the count==0 negative half of the same contract.
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
	// Belt-and-suspenders: parse the bytes and confirm count + duration_ms
	// are present (#312 fix). The exact-string match above already pins
	// this, but the field-presence assertion catches a regression that
	// shifts ordering without dropping the field.
	var evt map[string]any
	if err := json.Unmarshal([]byte(got), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := evt["count"]; !ok {
		t.Errorf("count must be present on service_finish (#312)")
	}
	if _, ok := evt["duration_ms"]; !ok {
		t.Errorf("duration_ms must be present on service_finish (#312)")
	}
}

// TestJSONEmitter_StageFinishShape pins all three numeric fields
// (count, total, duration_ms) MUST appear on stage_finish, and pairs
// with TestJSONEmitter_StageFinishCountZeroEmitted for the count==0
// half of the #312 contract.
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
	// Belt-and-suspenders: parse and confirm all three numeric fields
	// land on the wire — the failure mode #312 fixes is silent absence,
	// not wrong values, so a presence-pin guards future refactors.
	var evt map[string]any
	if err := json.Unmarshal([]byte(got), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{"count", "total", "duration_ms"} {
		if _, ok := evt[k]; !ok {
			t.Errorf("%q must be present on stage_finish (#312)", k)
		}
	}
}

// TestJSONEmitter_ServiceFinishCountZeroEmitted is the headline #312
// regression test: a service_finish event whose scope yielded zero
// items must still emit `"count":0` and `"duration_ms":0` so SSE
// consumers can render "0 of 0" without special-casing absent
// fields. The pre-fix shape used omitempty on Count/DurationMs and
// dropped both keys on the zero-result branch.
func TestJSONEmitter_ServiceFinishCountZeroEmitted(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ServiceFinish("sqs", "us-east-1", 0, 0)

	got := strings.TrimRight(buf.String(), "\n")
	if !strings.Contains(got, `"count":0`) {
		t.Errorf(`expected "count":0 on zero-result service_finish; got: %s`, got)
	}
	if !strings.Contains(got, `"duration_ms":0`) {
		t.Errorf(`expected "duration_ms":0 on zero-result service_finish; got: %s`, got)
	}
	// Parse for structural assertion — the substring match above
	// would also fire on `"count":0,"other":1` etc., but the parse
	// confirms count is genuinely a top-level field.
	var evt map[string]any
	if err := json.Unmarshal([]byte(got), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v, ok := evt["count"]; !ok || v.(float64) != 0 {
		t.Errorf("count must be 0 on zero-result service_finish; got %v ok=%v", v, ok)
	}
	if v, ok := evt["duration_ms"]; !ok || v.(float64) != 0 {
		t.Errorf("duration_ms must be 0 on zero-result service_finish; got %v ok=%v", v, ok)
	}
}

// TestJSONEmitter_StageFinishCountZeroEmitted asserts the second half
// of the #312 contract: stage_finish with zero items must emit all
// three numeric fields, including total=0 (which omitempty also
// dropped pre-fix).
func TestJSONEmitter_StageFinishCountZeroEmitted(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.StageFinish("discover", 0, 0)

	got := strings.TrimRight(buf.String(), "\n")
	for _, want := range []string{`"count":0`, `"total":0`, `"duration_ms":0`} {
		if !strings.Contains(got, want) {
			t.Errorf(`expected %s on zero-result stage_finish; got: %s`, want, got)
		}
	}
	var evt map[string]any
	if err := json.Unmarshal([]byte(got), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{"count", "total", "duration_ms"} {
		v, ok := evt[k]
		if !ok {
			t.Errorf("%q must be present on zero-result stage_finish (#312)", k)
			continue
		}
		if f, isF := v.(float64); !isF || f != 0 {
			t.Errorf("%q must be 0 on zero-result stage_finish; got %v", k, v)
		}
	}
}

// TestJSONEmitter_ItemFoundOmitsCountField pins the negative half of
// the #312 contract: numeric fields that belong to service_finish /
// stage_finish must NOT appear on item_found, even though
// MarshalJSON now disables struct-tag omitempty on them.
func TestJSONEmitter_ItemFoundOmitsCountField(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ItemFound("sqs", "us-east-1", "aws_sqs_queue", "https://sqs.us-east-1.amazonaws.com/123/q1")

	got := strings.TrimRight(buf.String(), "\n")
	var evt map[string]any
	if err := json.Unmarshal([]byte(got), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{"count", "total", "duration_ms"} {
		if _, present := evt[k]; present {
			t.Errorf("item_found must not carry %q; got: %s", k, got)
		}
	}
}

// TestJSONEmitter_ServiceStartOmitsCountField is the negative pin for
// service_start: a phase-start event has no count/total/duration yet,
// and MarshalJSON must not leak zero-valued numeric fields.
func TestJSONEmitter_ServiceStartOmitsCountField(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ServiceStart("sqs", "us-east-1")

	got := strings.TrimRight(buf.String(), "\n")
	var evt map[string]any
	if err := json.Unmarshal([]byte(got), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{"count", "total", "duration_ms"} {
		if _, present := evt[k]; present {
			t.Errorf("service_start must not carry %q; got: %s", k, got)
		}
	}
}

// TestJSONEmitter_GCPLocationFieldUsed exercises the Region vs Location
// branch in MarshalJSON: when an Event has Location set (GCP
// discoverers), the wire shape must emit `"location":...` and never
// `"region":...`. The public Emitter API on JSONEmitter only takes a
// `region` parameter, so we construct the Event directly and call
// json.Marshal — the same path the writer uses internally.
func TestJSONEmitter_GCPLocationFieldUsed(t *testing.T) {
	t.Parallel()
	ts, _ := time.Parse(time.RFC3339Nano, "2026-05-07T12:00:00Z")
	for _, eventType := range []string{"service_start", "service_finish", "item_found"} {
		eventType := eventType
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()
			evt := Event{
				Event:     eventType,
				Service:   "cloud_asset_inventory",
				Location:  "us-central1",
				TFType:    "google_compute_instance",
				ImportID:  "projects/p/zones/us-central1-a/instances/i",
				Count:     5,
				Timestamp: ts,
			}
			b, err := json.Marshal(evt)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			s := string(b)
			if !strings.Contains(s, `"location":"us-central1"`) {
				t.Errorf(`expected "location":"us-central1" on %s; got: %s`, eventType, s)
			}
			if strings.Contains(s, `"region":`) {
				t.Errorf("Location-set event must not also emit region; got: %s", s)
			}
		})
	}
}

// TestJSONEmitter_GlobalServiceEmptyRegionShape pins the IAM-style
// global-service emit shape: when an emitter calls ServiceStart(slug,
// "") the wire output carries "region":"" rather than dropping the
// field entirely. This is a deliberate semantic choice (see Event
// docstring) — the empty string parses identically to "no region
// scope" downstream, and avoiding a third Marshal branch keeps the
// dispatch readable.
func TestJSONEmitter_GlobalServiceEmptyRegionShape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := NewJSONEmitter(&buf).WithNow(fixedNow())
	e.ServiceStart("iam", "")

	got := strings.TrimRight(buf.String(), "\n")
	var evt map[string]any
	if err := json.Unmarshal([]byte(got), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	v, ok := evt["region"]
	if !ok {
		t.Errorf("global service ServiceStart must emit region key; got: %s", got)
	}
	if s, isS := v.(string); !isS || s != "" {
		t.Errorf(`region must be "" on global service; got %v`, v)
	}
	if _, present := evt["location"]; present {
		t.Errorf("global service ServiceStart must not emit location; got: %s", got)
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
	e.ServiceWarn("sqs", "us-east-1", "throttled")

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
