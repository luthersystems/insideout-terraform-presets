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
	return NewDiscoverEmitter(sink, nil)
}

// NewDiscoverEmitter is NewProgressEmitter plus an optional per-resource
// Found sink: when found is non-nil the bridge ALSO forwards each
// ItemFound tick (fired by the discoverers the moment each resource is
// found, mid-scan) as a DiscoverFound. This powers the live
// "resources as we find them" trickle on the reverse-import Scan panel.
// Both sinks nil → NopEmitter, byte-for-byte the no-progress path.
// Found invocations are serialized under the same mutex as TypeDone, so
// the sink is safe to call from the parallel per-type scan goroutines.
func NewDiscoverEmitter(sink func(DiscoverProgress), found func(DiscoverFound)) progress.Emitter {
	if sink == nil && found == nil {
		return progress.NopEmitter{}
	}
	return &progressBridge{sink: sink, found: found}
}

// progressBridge adapts a per-type DiscoverProgress sink onto the
// progress.Emitter contract the discover / enrich orchestrators consume.
// It implements the base Emitter as no-ops (the facade's progress
// contract is per-type, not per-(service,region)) and the optional
// progress.TypeProgressEmitter to receive and forward the per-type
// completion events.
type progressBridge struct {
	sink  func(DiscoverProgress)
	found func(DiscoverFound)

	mu        sync.Mutex
	completed int // running count of types done; guarded by mu
}

// Compile-time checks: *progressBridge satisfies both the base Emitter
// and the optional per-type extension interface.
var (
	_ progress.Emitter             = (*progressBridge)(nil)
	_ progress.TypeProgressEmitter = (*progressBridge)(nil)
)

// The per-(service,region) Emitter events other than ItemFound are not
// part of the facade's contract, so the bridge swallows them.
func (b *progressBridge) ServiceStart(string, string)                      {}
func (b *progressBridge) ServiceFinish(string, string, int, time.Duration) {}
func (b *progressBridge) StageFinish(string, int, time.Duration)           {}
func (b *progressBridge) ServiceWarn(string, string, string)               {}

// ItemFound forwards each per-resource discovery tick to the optional
// Found sink (NewDiscoverEmitter), serialized under the same mutex as
// TypeDone so a single consumer-side sink needs no locking. Phase is
// fixed to "discover": providers wire the Found sink only on the
// Discover leg (EnrichAttributes keeps the progress-only emitter), and
// the enrich pass's own ItemFound ticks identify themselves via the
// "enrich" service slug anyway. Nil sink (NewProgressEmitter callers)
// keeps the historical swallow.
func (b *progressBridge) ItemFound(service, region, tfType, importID string) {
	if b.found == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.found(DiscoverFound{
		Phase:    "discover",
		Service:  service,
		Region:   region,
		Type:     tfType,
		ImportID: importID,
	})
}

// TypeDone increments the running completed-type counter and forwards a
// DiscoverProgress to the sink. The lock both serializes concurrent
// callers (the AWS discover walk fans out per-type goroutines) and makes
// the increment-then-deliver atomic, so the sink observes CompletedTypes
// as a strictly monotonic 1..Total sequence.
func (b *progressBridge) TypeDone(p progress.TypeProgress) {
	if b.sink == nil {
		// Found-only bridge (NewDiscoverEmitter(nil, found)): no per-type
		// progress consumer, so swallow — matching the pre-bridge NopEmitter.
		return
	}
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
