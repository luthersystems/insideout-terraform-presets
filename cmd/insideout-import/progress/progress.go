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

// Event is the on-the-wire shape produced by JSONEmitter. Each event
// type carries a specific, fixed field set defined by the
// MarshalJSON dispatch below; the on-the-wire field order follows the
// per-event branch in MarshalJSON, with `event` always first and `ts`
// always last so a streaming consumer can route on `event` without
// reading the rest of the line.
//
// The struct tags carry no `omitempty` on the numeric fields
// (Count/Total/DurationMs) because MarshalJSON elides absent fields
// per event type rather than relying on Go's zero-value-elision.
// This is the fix for #312: a service_finish or stage_finish whose
// scope yielded zero items must still emit `"count":0` so SSE
// consumers don't have to special-case "count missing" vs
// "count == 0". The Timestamp field is always present; tests inject
// a fixed clock via JSONEmitter.WithNow so golden output is
// deterministic.
//
// Region vs Location: today AWS discoverers populate Region and GCP
// discoverers populate Location (the public Emitter API takes a
// `region` parameter that GCP overloads with location values, but
// the Event struct keeps them distinct for downstream consumers).
// MarshalJSON emits exactly one of `region` or `location` on
// service_*/item_found events: Location wins if both are set
// (defensive), Region is emitted otherwise. If both are empty
// (e.g. global services like IAM that pass region=""), MarshalJSON
// emits `"region":""` — preserving the previous omitempty-absent
// shape would require a third branch and the empty string is
// already semantically "no region scope", which downstream parses
// the same way.
type Event struct {
	Event      string    `json:"event"`               // service_start | service_finish | item_found | stage_finish | service_warn
	Service    string    `json:"service,omitempty"`   // AWS service slug (sqs, dynamodb, ...) or GCP asset_type slug (cloud_asset_inventory)
	Region     string    `json:"region,omitempty"`    // AWS region; empty for global services (IAM, S3) and GCP
	Location   string    `json:"location,omitempty"`  // GCP location; empty for project-global types and AWS
	TFType     string    `json:"tf_type,omitempty"`   // Terraform resource type for item_found
	ImportID   string    `json:"import_id,omitempty"` // Terraform import ID for item_found
	Stage      string    `json:"stage,omitempty"`     // discover for stage_finish (room for future stages: hcl_gen, drift_fix, dep_chase)
	Message    string    `json:"message,omitempty"`   // service_warn — operator-facing soft-fail description
	Count      int       `json:"count"`               // service_finish, stage_finish — items emitted in scope; always emitted on those events even when zero (#312)
	Total      int       `json:"total"`               // stage_finish — total items across the stage (==Count today, separate field for future fan-in); always emitted on stage_finish even when zero (#312)
	DurationMs int64     `json:"duration_ms"`         // service_finish, stage_finish — wall-time spent in the scope; always emitted on those events even when zero (#312)
	Timestamp  time.Time `json:"ts"`                  // every event; injectable for deterministic tests via WithNow
}

// MarshalJSON implements per-event-type field dispatch so each event
// carries exactly the field set its type requires (#312). The
// per-event branches build typed structs (rather than a
// map[string]any) so output preserves declaration order — every
// emit starts with `event` and ends with `ts`, with the
// type-specific fields between in the order documented in the
// issue body. This is purely a readability concern; JSON consumers
// parse objects unordered.
//
// The fix for #312 is structural: the typed event structs declare
// Count/Total/DurationMs WITHOUT `omitempty`, so a service_finish
// or stage_finish whose scope yielded zero items still emits
// `"count":0`. The previous shared-struct + omitempty approach
// dropped those fields and forced SSE consumers to special-case
// "count missing" vs "count == 0".
//
// Field-set contract by event type:
//
//	service_start:  event, service, (region|location), ts
//	service_finish: event, service, (region|location), count, duration_ms, ts
//	item_found:     event, service, (region|location), tf_type, import_id, ts
//	stage_finish:   event, stage, count, total, duration_ms, ts
//
// Region/Location selection: prefer Location when set (GCP
// emitters), otherwise emit Region (AWS emitters and global
// services). Empty Region for global AWS services produces
// `"region":""`, matching the existing IAM emit shape. An unknown
// Event value falls back to a struct-alias marshal so we don't
// silently drop fields if the contract grows.
func (e Event) MarshalJSON() ([]byte, error) {
	switch e.Event {
	case "service_start":
		if e.Location != "" {
			return json.Marshal(struct {
				Event     string    `json:"event"`
				Service   string    `json:"service"`
				Location  string    `json:"location"`
				Timestamp time.Time `json:"ts"`
			}{e.Event, e.Service, e.Location, e.Timestamp})
		}
		return json.Marshal(struct {
			Event     string    `json:"event"`
			Service   string    `json:"service"`
			Region    string    `json:"region"`
			Timestamp time.Time `json:"ts"`
		}{e.Event, e.Service, e.Region, e.Timestamp})
	case "service_finish":
		if e.Location != "" {
			return json.Marshal(struct {
				Event      string    `json:"event"`
				Service    string    `json:"service"`
				Location   string    `json:"location"`
				Count      int       `json:"count"`
				DurationMs int64     `json:"duration_ms"`
				Timestamp  time.Time `json:"ts"`
			}{e.Event, e.Service, e.Location, e.Count, e.DurationMs, e.Timestamp})
		}
		return json.Marshal(struct {
			Event      string    `json:"event"`
			Service    string    `json:"service"`
			Region     string    `json:"region"`
			Count      int       `json:"count"`
			DurationMs int64     `json:"duration_ms"`
			Timestamp  time.Time `json:"ts"`
		}{e.Event, e.Service, e.Region, e.Count, e.DurationMs, e.Timestamp})
	case "item_found":
		if e.Location != "" {
			return json.Marshal(struct {
				Event     string    `json:"event"`
				Service   string    `json:"service"`
				Location  string    `json:"location"`
				TFType    string    `json:"tf_type"`
				ImportID  string    `json:"import_id"`
				Timestamp time.Time `json:"ts"`
			}{e.Event, e.Service, e.Location, e.TFType, e.ImportID, e.Timestamp})
		}
		return json.Marshal(struct {
			Event     string    `json:"event"`
			Service   string    `json:"service"`
			Region    string    `json:"region"`
			TFType    string    `json:"tf_type"`
			ImportID  string    `json:"import_id"`
			Timestamp time.Time `json:"ts"`
		}{e.Event, e.Service, e.Region, e.TFType, e.ImportID, e.Timestamp})
	case "stage_finish":
		return json.Marshal(struct {
			Event      string    `json:"event"`
			Stage      string    `json:"stage"`
			Count      int       `json:"count"`
			Total      int       `json:"total"`
			DurationMs int64     `json:"duration_ms"`
			Timestamp  time.Time `json:"ts"`
		}{e.Event, e.Stage, e.Count, e.Total, e.DurationMs, e.Timestamp})
	case "service_warn":
		// service_warn carries only (service, region, message) — the
		// Emitter interface does not surface a location form. If a
		// future warn callsite needs a GCP location it should extend
		// the Emitter contract first.
		return json.Marshal(struct {
			Event     string    `json:"event"`
			Service   string    `json:"service"`
			Region    string    `json:"region"`
			Message   string    `json:"message"`
			Timestamp time.Time `json:"ts"`
		}{e.Event, e.Service, e.Region, e.Message, e.Timestamp})
	default:
		// Unknown event type — defensive fallback. Marshal the
		// struct verbatim via an alias to avoid recursing back
		// into this method. The struct-tag omitempty on the
		// optional fields suppresses zero-value noise for unknown
		// shapes (where we have no per-event contract to honor).
		type alias Event
		return json.Marshal(alias(e))
	}
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
	// ServiceWarn surfaces a non-fatal warning during a service's
	// discovery scope (e.g. a per-instance soft-fail in the non-CAI
	// SQL user fanout — see #396 / #383). The orchestrator's
	// soft-fail paths used stderr-only logging before this method;
	// routing through the Emitter ensures the UI's progress stream
	// receives the same signal a JSON/SSE consumer would expect.
	ServiceWarn(service, region, msg string)
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

// ServiceWarn is a no-op.
func (NopEmitter) ServiceWarn(string, string, string) {}

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

// ServiceWarn emits one service_warn event surfacing a non-fatal
// soft-fail during a discoverer's scope. service/region match the
// surrounding ServiceStart/ServiceFinish bracketing; msg is an
// operator-facing description (e.g. "list failed for instance N3
// in project P: ...."). Best-effort like the other emit methods.
func (e *JSONEmitter) ServiceWarn(service, region, msg string) {
	e.write(Event{
		Event:     "service_warn",
		Service:   service,
		Region:    region,
		Message:   msg,
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
