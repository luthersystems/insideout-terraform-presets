package gcpdiscover

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
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

// bucketedFakeSearcher is a fakeAssetSearcher that returns a different
// result slice per call, indexed by the call's first asset-type. Used
// by mixed-bucket tests that need the labels-style and name-prefix-style
// SearchAll calls to surface distinct rows so the orchestrator's
// per-bucket assembly + client-side filter can be pinned independently.
//
// The two-bucket dispatch in DiscoverTypes is sequential today
// (gcpdiscover.go's searchBuckets calls labels-bucket then name-
// prefix-bucket), so the mutex is a no-op in practice — but pinning
// `calls` under a lock matches the recordingEmitter sibling and stays
// safe against a future refactor that parallelizes the two SearchAll
// invocations.
type bucketedFakeSearcher struct {
	resultsByAssetType map[string][]gcpAssetResult

	mu    sync.Mutex
	calls []searchAllCall
}

func (b *bucketedFakeSearcher) SearchAll(_ context.Context, scope string, assetTypes []string, query string) ([]gcpAssetResult, error) {
	cp := make([]string, len(assetTypes))
	copy(cp, assetTypes)
	b.mu.Lock()
	b.calls = append(b.calls, searchAllCall{scope: scope, assetTypes: cp, query: query})
	b.mu.Unlock()
	var out []gcpAssetResult
	for _, at := range assetTypes {
		out = append(out, b.resultsByAssetType[at]...)
	}
	return out, nil
}

// fakeNamePrefixDiscoverer is the unit-test counterpart to the
// label-style discoverers — it returns ScopeStyleNamePrefix so the
// orchestrator routes its asset-type into the name-prefix bucket. Used
// to exercise the two-bucket dispatch surface introduced in #366
// without registering a real label-less type (those land in PRs 2+).
//
// resourceType and assetType are constructor params so multiple
// fakes can co-register in one test without colliding.
type fakeNamePrefixDiscoverer struct {
	resourceType string
	assetType    string
}

func (d *fakeNamePrefixDiscoverer) ResourceType() string   { return d.resourceType }
func (d *fakeNamePrefixDiscoverer) AssetType() string      { return d.assetType }
func (d *fakeNamePrefixDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (d *fakeNamePrefixDiscoverer) FromAsset(_ addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:     "gcp",
			Type:      d.resourceType,
			Address:   fmt.Sprintf("%s.%s", d.resourceType, name),
			ImportID:  name,
			NameHint:  name,
			ProjectID: projectID,
			Location:  a.Location,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
}

func (d *fakeNamePrefixDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _ string, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, ErrNotSupported
}
