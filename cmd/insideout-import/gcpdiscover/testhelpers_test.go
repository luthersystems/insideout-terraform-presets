package gcpdiscover

import (
	"context"
	"sync"
	"time"
)

// recordedEvent is a single emit observed by recordingEmitter (#295).
// Mirrors the awsdiscover-package helper of the same name; kept
// per-package so each cloud's test suite can assert independently.
type recordedEvent struct {
	Kind     string
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
// emit. Concurrent emissions are guarded by mu (Cloud Asset's per-asset
// translation is sequential today, but the helper stays lock-safe so
// regressions that introduce parallelism don't silently race).
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

func (r *recordingEmitter) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

// fakeAssetSearcher is the unit-test seam that replaces RealAssetSearcher.
// Tests configure `pages` (the canned response slice) and `err` (forced
// failure). Each SearchAll call appends to `calls` so assertions can pin
// the scope, asset-types, and query the discoverer threaded through.
type fakeAssetSearcher struct {
	results []gcpAssetResult
	err     error

	calls []searchAllCall
}

type searchAllCall struct {
	scope      string
	assetTypes []string
	query      string
}

func (f *fakeAssetSearcher) SearchAll(_ context.Context, scope string, assetTypes []string, query string) ([]gcpAssetResult, error) {
	cp := make([]string, len(assetTypes))
	copy(cp, assetTypes)
	f.calls = append(f.calls, searchAllCall{scope: scope, assetTypes: cp, query: query})
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}
