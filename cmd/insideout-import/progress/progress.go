// Package progress emits structured discovery progress events from the
// insideout-import discover subcommand. It powers the streaming
// `--progress=json` flag added in #295 (Bundle 2 / PR 1 of #289) so the
// reliable agent-API can translate per-service phase events into SSE for
// the v2 importer wizard's DiscoveryScreen ("SSE streams resource events").
//
// The contract is intentionally small: four event types
// (service_start, service_finish, item_found, stage_finish), each carrying
// the fields the downstream UI needs to render per-service progress bars
// and per-item rows. The Emitter interface is implemented by JSONEmitter
// (newline-delimited JSON to a writer) and NopEmitter (zero overhead when
// the flag is unset). Per-service discoverers always call methods on a
// non-nil Emitter — the orchestrator resolves nil to NopEmitter once at
// the top of DiscoverTypes so the per-service code never nil-checks.
//
// JSONEmitter serializes concurrent emissions through a sync.Mutex: the
// AWS DynamoDB and Lambda discoverers run per-item tag fetches under a
// bounded errgroup, so item_found events from those goroutines can race
// with one another and with parallel ServiceStart/ServiceFinish from the
// orchestrator. The lock guarantees each newline-delimited line is a
// complete JSON object and that the single Encode-then-flush write is
// not interleaved with another goroutine's write.
package progress

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Event is the on-the-wire shape produced by JSONEmitter. Field order in
// the JSON output is determined by the struct layout (Go's encoding/json
// emits in declaration order); we keep the most-discriminating fields
// first so a streaming consumer can route on `event` without reading the
// rest of the line.
//
// Optional fields use `omitempty` so each event type carries only the
// fields that apply to it — service_start has no Count/DurationMs, etc.
// The Timestamp field is always present; tests inject a fixed clock via
// JSONEmitter.WithNow so golden output is deterministic.
type Event struct {
	Event      string    `json:"event"`                 // service_start | service_finish | item_found | stage_finish
	Service    string    `json:"service,omitempty"`     // AWS service slug (sqs, dynamodb, ...) or GCP asset_type slug (cloud_asset_inventory)
	Region     string    `json:"region,omitempty"`      // AWS region; empty for global services (IAM, S3) and GCP
	Location   string    `json:"location,omitempty"`    // GCP location; empty for project-global types and AWS
	TFType     string    `json:"tf_type,omitempty"`     // Terraform resource type for item_found
	ImportID   string    `json:"import_id,omitempty"`   // Terraform import ID for item_found
	Stage      string    `json:"stage,omitempty"`       // discover for stage_finish (room for future stages: hcl_gen, drift_fix, dep_chase)
	Count      int       `json:"count,omitempty"`       // service_finish, stage_finish — items emitted in scope
	Total      int       `json:"total,omitempty"`       // stage_finish — total items across the stage (==Count today, separate field for future fan-in)
	DurationMs int64     `json:"duration_ms,omitempty"` // service_finish, stage_finish — wall-time spent in the scope
	Timestamp  time.Time `json:"ts"`                    // every event; injectable for deterministic tests via WithNow
}

// Emitter is the per-service progress contract. Default implementation
// (NopEmitter) is a no-op; JSONEmitter writes one newline-delimited JSON
// event per call. Method names mirror the issue body: ServiceStart and
// ServiceFinish bracket each (service, region) discovery scope;
// ItemFound fires once per ImportedResource the discoverer emits;
// StageFinish fires once at the end of DiscoverTypes with the total
// item count and wall-time.
type Emitter interface {
	ServiceStart(service, region string)
	ServiceFinish(service, region string, count int, dur time.Duration)
	ItemFound(service, region, tfType, importID string)
	StageFinish(stage string, total int, dur time.Duration)
}

// NopEmitter is a zero-overhead Emitter that swallows every call. The
// orchestrator substitutes NopEmitter{} when the operator did not pass
// --progress=json, so per-service discoverers always have a non-nil
// Emitter to call.
type NopEmitter struct{}

// ServiceStart is a no-op.
func (NopEmitter) ServiceStart(string, string) {}

// ServiceFinish is a no-op.
func (NopEmitter) ServiceFinish(string, string, int, time.Duration) {}

// ItemFound is a no-op.
func (NopEmitter) ItemFound(string, string, string, string) {}

// StageFinish is a no-op.
func (NopEmitter) StageFinish(string, int, time.Duration) {}

// JSONEmitter writes one newline-delimited JSON Event per call to an
// io.Writer. Concurrent emissions are serialized through mu so each
// line in the output stream is a complete JSON object.
//
// Construct with NewJSONEmitter; the zero value is not usable (the
// internal clock would emit the zero time on every event).
type JSONEmitter struct {
	out io.Writer
	now func() time.Time
	mu  sync.Mutex
}

// NewJSONEmitter constructs a JSONEmitter that writes to out and
// timestamps events with time.Now. Tests that need deterministic
// timestamps should call WithNow on the returned emitter.
func NewJSONEmitter(out io.Writer) *JSONEmitter {
	return &JSONEmitter{
		out: out,
		now: time.Now,
	}
}

// WithNow overrides the clock used to stamp Event.Timestamp. Returns
// the receiver so callers can chain. Test-only path: production code
// constructs with NewJSONEmitter and never calls WithNow.
func (e *JSONEmitter) WithNow(now func() time.Time) *JSONEmitter {
	e.now = now
	return e
}

// ServiceStart emits one service_start event. Errors writing to the
// underlying io.Writer are silently dropped: a failed progress emit
// must never abort the discovery run, since the caller (reliable
// agent-API) is consuming a best-effort stream and the discovery
// stages own their own error reporting.
func (e *JSONEmitter) ServiceStart(service, region string) {
	e.write(Event{
		Event:     "service_start",
		Service:   service,
		Region:    region,
		Timestamp: e.now(),
	})
}

// ServiceFinish emits one service_finish event with the final item
// count for the (service, region) scope and the wall-time spent in
// that scope.
func (e *JSONEmitter) ServiceFinish(service, region string, count int, dur time.Duration) {
	e.write(Event{
		Event:      "service_finish",
		Service:    service,
		Region:     region,
		Count:      count,
		DurationMs: dur.Milliseconds(),
		Timestamp:  e.now(),
	})
}

// ItemFound emits one item_found event per ImportedResource. tfType
// and importID match the Identity.Type and Identity.ImportID stamped
// onto the resource by makeImportedResource.
func (e *JSONEmitter) ItemFound(service, region, tfType, importID string) {
	e.write(Event{
		Event:     "item_found",
		Service:   service,
		Region:    region,
		TFType:    tfType,
		ImportID:  importID,
		Timestamp: e.now(),
	})
}

// StageFinish emits one stage_finish event at the end of DiscoverTypes
// with the aggregate count + wall-time across every (service, region)
// scope. The Stage field is "discover" today; reserved as a discriminator
// for future per-stage events (Stage 2b genconfig, Stage 2c1 driftfix,
// Stage 2c3 depchase).
func (e *JSONEmitter) StageFinish(stage string, total int, dur time.Duration) {
	e.write(Event{
		Event:      "stage_finish",
		Stage:      stage,
		Count:      total,
		Total:      total,
		DurationMs: dur.Milliseconds(),
		Timestamp:  e.now(),
	})
}

// write encodes evt as a single JSON object followed by a newline and
// writes it to e.out under the lock. Encoding errors are silently
// dropped (best-effort stream — see ServiceStart docs).
//
// We use json.Marshal + a single Write rather than json.NewEncoder
// because the Encoder writes the trailing newline as a separate Write
// call, which under contention can interleave with another goroutine's
// next event payload even with the mutex held — Encoder's two writes
// would atomically span the lock-and-release boundary. Marshal +
// fmt.Fprintln is one Write to e.out, one byte sequence.
func (e *JSONEmitter) write(evt Event) {
	buf, err := json.Marshal(evt)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = fmt.Fprintln(e.out, string(buf))
}
