package awsdiscover

import (
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// awsDummyConfig returns an aws.Config with no real credentials. Tests
// that build the production AWSDiscoverer just to inspect its registry
// (e.g. TestNewAWSDiscoverer_Registers5PhaseOneTypes) need *some* config
// to call NewAWSDiscoverer; they do not perform any SDK calls.
func awsDummyConfig() aws.Config { return aws.Config{Region: "us-east-1"} }

// recordedEvent is a single emit observed by recordingEmitter (#295). The
// fields cover every progress.Emitter method's load-bearing arguments;
// per-method tests pin the relevant subset.
type recordedEvent struct {
	Kind     string // "service_start" | "service_finish" | "item_found" | "stage_finish"
	Service  string
	Region   string
	TFType   string
	ImportID string
	Stage    string
	Count    int
	Total    int
	Dur      time.Duration
}

// recordingEmitter is a test-only progress.Emitter that captures every
// emit into an in-memory slice. It mirrors the semantics of
// progress.NopEmitter for the per-service Discover code path (Emitter is
// always non-nil after the emitterOrNop fallback) but lets tests assert
// the sequence and field values of emitted events.
//
// Concurrent emissions are guarded by mu — DynamoDB and Lambda discoverers
// fan out per-item tag fetches under an errgroup, so item_found events
// can race.
type recordingEmitter struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (r *recordingEmitter) ServiceStart(service, region string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Kind: "service_start", Service: service, Region: region})
}

func (r *recordingEmitter) ServiceFinish(service, region string, count int, dur time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Kind: "service_finish", Service: service, Region: region, Count: count, Dur: dur})
}

func (r *recordingEmitter) ItemFound(service, region, tfType, importID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Kind: "item_found", Service: service, Region: region, TFType: tfType, ImportID: importID})
}

func (r *recordingEmitter) StageFinish(stage string, total int, dur time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Kind: "stage_finish", Stage: stage, Total: total, Count: total, Dur: dur})
}

// snapshot returns a copy of the event slice under lock so test
// assertions don't race with any concurrent emits still in flight.
func (r *recordingEmitter) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}
