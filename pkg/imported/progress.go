package imported

import (
	"sync"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
)

// NewProgressEmitter returns the progress.Emitter that the per-cloud
// Provider implementations hand to the underlying discoverer for a
// Discover / EnrichAttributes call, bridging the cloud-agnostic
// DiscoverProgress sink onto the discoverer's per-type completion events
// (#699).
//
//   - sink == nil → progress.NopEmitter{}: zero-overhead, and the
//     orchestrators' TypeProgressEmitter type-assertion fails, so the
//     per-type emission path is skipped entirely. This is the today's-
//     behavior, byte-for-byte path.
//   - sink != nil → a bridge that no-ops the per-(service,region) Emitter
//     events (not part of the facade contract) and forwards each per-type
//     TypeDone to sink as a DiscoverProgress.
//
// The bridge serializes sink invocations under a mutex and owns the
// monotonic CompletedTypes counter, so a sink is safe to call from the
// AWS discover path's parallel per-type walk and always observes
// CompletedTypes incrementing 1..TotalTypes in order.
//
// Exported because pkg/imported/aws and pkg/imported/gcp are distinct
// packages that both need to construct it; facade consumers (e.g.
// reliable's importer wizard) never call it directly — they set
// DiscoverOpts.Progress / EnrichOpts.Progress and let the Provider wire
// it up.
func NewProgressEmitter(sink func(DiscoverProgress)) progress.Emitter {
	if sink == nil {
		return progress.NopEmitter{}
	}
	return &progressBridge{sink: sink}
}

// progressBridge adapts a per-type DiscoverProgress sink onto the
// progress.Emitter contract the discover / enrich orchestrators consume.
// It implements the base Emitter as no-ops (the facade's progress
// contract is per-type, not per-(service,region)) and the optional
// progress.TypeProgressEmitter to receive and forward the per-type
// completion events.
type progressBridge struct {
	sink func(DiscoverProgress)

	mu        sync.Mutex
	completed int // running count of types done; guarded by mu
}

// Compile-time checks: *progressBridge satisfies both the base Emitter
// and the optional per-type extension interface.
var (
	_ progress.Emitter             = (*progressBridge)(nil)
	_ progress.TypeProgressEmitter = (*progressBridge)(nil)
)

// The per-(service,region) Emitter events are not part of the facade's
// per-type progress contract, so the bridge swallows them.
func (b *progressBridge) ServiceStart(string, string)                      {}
func (b *progressBridge) ServiceFinish(string, string, int, time.Duration) {}
func (b *progressBridge) ItemFound(string, string, string, string)         {}
func (b *progressBridge) StageFinish(string, int, time.Duration)           {}
func (b *progressBridge) ServiceWarn(string, string, string)               {}

// TypeDone increments the running completed-type counter and forwards a
// DiscoverProgress to the sink. The lock both serializes concurrent
// callers (the AWS discover walk fans out per-type goroutines) and makes
// the increment-then-deliver atomic, so the sink observes CompletedTypes
// as a strictly monotonic 1..Total sequence.
func (b *progressBridge) TypeDone(p progress.TypeProgress) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.completed++
	b.sink(DiscoverProgress{
		Phase:          p.Phase,
		Type:           p.TFType,
		FoundCount:     p.Found,
		CompletedTypes: b.completed,
		TotalTypes:     p.Total,
	})
}
