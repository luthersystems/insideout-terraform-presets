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
	Message  string
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

func (r *recordingEmitter) ServiceWarn(service, region, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Kind: "service_warn", Service: service, Region: region, Message: msg})
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

// fakeParentNamePrefixDiscoverer is the unit-test counterpart to
// fakeNamePrefixDiscoverer for the parent-name-prefix bucket (#381):
// it returns ScopeStyleParentNamePrefix and implements
// parentScopedDiscoverer.ParentMarker() so the orchestrator routes
// its asset-type into the parent bucket and the third-bucket post-
// filter is exercised end-to-end without registering a real
// child-of-parent type.
//
// resourceType, assetType, and parentMarker are constructor params
// so multiple fakes can co-register in one test with distinct
// markers (e.g. "/parents1/" vs "/parents2/") to pin per-discoverer
// marker dispatch.
type fakeParentNamePrefixDiscoverer struct {
	resourceType string
	assetType    string
	parentMarker string
}

func (d *fakeParentNamePrefixDiscoverer) ResourceType() string   { return d.resourceType }
func (d *fakeParentNamePrefixDiscoverer) AssetType() string      { return d.assetType }
func (d *fakeParentNamePrefixDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleParentNamePrefix }
func (d *fakeParentNamePrefixDiscoverer) ParentMarker() string   { return d.parentMarker }

func (d *fakeParentNamePrefixDiscoverer) FromAsset(_ addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
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

func (d *fakeParentNamePrefixDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _ string, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, ErrNotSupported
}

// brokenParentScopeDiscoverer reports ScopeStyleParentNamePrefix but
// does NOT implement the parentScopedDiscoverer side-interface (no
// ParentMarker method). Used by
// TestSearchBuckets_ParentMissingSideInterface_FailsLoud to pin the
// programmer-error path at gcpdiscover.go::searchBuckets — without a
// fault-injection fake, the live registry's contract test cannot
// reach those error returns at runtime, so a refactor that demoted
// them to a silent continue would ship green.
type brokenParentScopeDiscoverer struct {
	resourceType string
	assetType    string
}

func (d *brokenParentScopeDiscoverer) ResourceType() string   { return d.resourceType }
func (d *brokenParentScopeDiscoverer) AssetType() string      { return d.assetType }
func (d *brokenParentScopeDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleParentNamePrefix }

func (d *brokenParentScopeDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (d *brokenParentScopeDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _ string, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, ErrNotSupported
}

// fakeLoggingSinkLister returns a canned set of sinks for the unit
// tests of loggingProjectSinkDiscoverer. Each test sets `sinks` (the
// canned response) and inspects `calls` after the discoverer runs.
type fakeLoggingSinkLister struct {
	sinks []gcpLoggingSink
	err   error
	calls []string
}

func (f *fakeLoggingSinkLister) ListSinks(_ context.Context, projectID string) ([]gcpLoggingSink, error) {
	f.calls = append(f.calls, projectID)
	if f.err != nil {
		return nil, f.err
	}
	return f.sinks, nil
}

// fakeSQLUserLister returns canned users per-instance. usersByInstance
// keys on the instance name; an instance not in the map yields zero
// users (legitimate empty state).
type fakeSQLUserLister struct {
	usersByInstance map[string][]gcpSQLUser
	errByInstance   map[string]error
	calls           []struct {
		projectID string
		instance  string
	}
}

func (f *fakeSQLUserLister) ListSQLUsers(_ context.Context, projectID, instance string) ([]gcpSQLUser, error) {
	f.calls = append(f.calls, struct {
		projectID string
		instance  string
	}{projectID, instance})
	if err, ok := f.errByInstance[instance]; ok {
		return nil, err
	}
	return f.usersByInstance[instance], nil
}

// fakeIdentityPlatformConfigLister returns either a canned config or
// nil-without-error (the "Identity Platform not activated" state).
type fakeIdentityPlatformConfigLister struct {
	cfg   *gcpIdentityPlatformConfig
	err   error
	calls []string
}

func (f *fakeIdentityPlatformConfigLister) GetIdentityPlatformConfig(_ context.Context, projectID string) (*gcpIdentityPlatformConfig, error) {
	f.calls = append(f.calls, projectID)
	if f.err != nil {
		return nil, f.err
	}
	return f.cfg, nil
}

// fakeIAMPolicyLister fronts the Bundle G1 (#470) per-parent
// GetIamPolicy probes. Per-method canned-response maps keyed on the
// per-parent identifier (`bindingsBySecret["projects/p/secrets/s"]`)
// keep tests compact, and per-method error-injection maps
// (`errBySecret[...] = ...`) let a test pin the soft-fail path
// without forcing all parents into the error branch. Each method
// records its calls in a per-method slice so assertions can pin the
// fan-out shape (one call per parent vs single project query).
//
// `errProject` is the singleton error knob for GetProjectIAMPolicy —
// the project lister has no per-key dimension, so a single bool is
// enough.
type fakeIAMPolicyLister struct {
	bindingsProject map[string][]gcpIAMBinding
	errProject      map[string]error
	callsProject    []string

	bindingsBySecret map[string][]gcpIAMBinding
	errBySecret      map[string]error
	callsBySecret    []string

	bindingsByKey map[string][]gcpIAMBinding
	errByKey      map[string]error
	callsByKey    []string

	bindingsByService map[string][]gcpIAMBinding
	errByService      map[string]error
	callsByService    []string

	bindingsByFunction map[string][]gcpIAMBinding
	errByFunction      map[string]error
	callsByFunction    []string

	bindingsByBucket map[string][]gcpIAMBinding
	errByBucket      map[string]error
	callsByBucket    []string
}

func (f *fakeIAMPolicyLister) GetProjectIAMPolicy(_ context.Context, projectID string) ([]gcpIAMBinding, error) {
	f.callsProject = append(f.callsProject, projectID)
	if err, ok := f.errProject[projectID]; ok {
		return nil, err
	}
	return f.bindingsProject[projectID], nil
}

func (f *fakeIAMPolicyLister) GetSecretIAMPolicy(_ context.Context, secretFullName string) ([]gcpIAMBinding, error) {
	f.callsBySecret = append(f.callsBySecret, secretFullName)
	if err, ok := f.errBySecret[secretFullName]; ok {
		return nil, err
	}
	return f.bindingsBySecret[secretFullName], nil
}

func (f *fakeIAMPolicyLister) GetKMSCryptoKeyIAMPolicy(_ context.Context, keyFullName string) ([]gcpIAMBinding, error) {
	f.callsByKey = append(f.callsByKey, keyFullName)
	if err, ok := f.errByKey[keyFullName]; ok {
		return nil, err
	}
	return f.bindingsByKey[keyFullName], nil
}

func (f *fakeIAMPolicyLister) GetCloudRunV2ServiceIAMPolicy(_ context.Context, serviceFullName string) ([]gcpIAMBinding, error) {
	f.callsByService = append(f.callsByService, serviceFullName)
	if err, ok := f.errByService[serviceFullName]; ok {
		return nil, err
	}
	return f.bindingsByService[serviceFullName], nil
}

func (f *fakeIAMPolicyLister) GetCloudFunctions2FunctionIAMPolicy(_ context.Context, fnFullName string) ([]gcpIAMBinding, error) {
	f.callsByFunction = append(f.callsByFunction, fnFullName)
	if err, ok := f.errByFunction[fnFullName]; ok {
		return nil, err
	}
	return f.bindingsByFunction[fnFullName], nil
}

func (f *fakeIAMPolicyLister) GetBucketIAMPolicy(_ context.Context, bucketName string) ([]gcpIAMBinding, error) {
	f.callsByBucket = append(f.callsByBucket, bucketName)
	if err, ok := f.errByBucket[bucketName]; ok {
		return nil, err
	}
	return f.bindingsByBucket[bucketName], nil
}
