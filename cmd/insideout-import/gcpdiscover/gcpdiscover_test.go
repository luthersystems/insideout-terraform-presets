package gcpdiscover

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// expectedRegisteredTypes is the contract source-of-truth for which
// Terraform types the live constructor map carries. Add a row when a
// new discoverer ships (PR 2-12 of Bundle 8 grows this set); the
// parity test below is what blocks unintended drift.
//
// Keeping a single allowlist is intentionally tedious: every new type
// must be added here explicitly, which is the friction we want — an
// accidental registration in NewGCPDiscoverer surfaces as a test
// failure, not silent behavior change.
var expectedRegisteredTypes = map[string]bool{
	"google_pubsub_topic":          false,
	"google_pubsub_subscription":   false,
	"google_storage_bucket":        false,
	"google_secret_manager_secret": false,
	"google_compute_network":       false,
	"google_service_account":       false,
	"google_kms_key_ring":          false,
	"google_kms_crypto_key":        false,
	"google_compute_firewall":      false,
	"google_compute_router":        false,
	"google_compute_address":       false,
	"google_compute_instance":      false,
	"google_container_cluster":     false,
	"google_container_node_pool":   false,
}

func TestNewGCPDiscoverer_RegistersExpectedTypes(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "real-proj")
	got := g.SupportedTypes()
	want := make(map[string]bool, len(expectedRegisteredTypes))
	for k, v := range expectedRegisteredTypes {
		want[k] = v
	}
	for _, typ := range got {
		if _, ok := want[typ]; !ok {
			t.Errorf("unexpected registered type %q", typ)
		}
		want[typ] = true
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("expected %q to be registered", k)
		}
	}
}

func TestSupportedTypes_IsSorted(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "p")
	got := g.SupportedTypes()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("SupportedTypes() not sorted: %q comes before %q", got[i-1], got[i])
		}
	}
}

func TestDiscoverTypes_ScopesToProjectAndPassesAssetTypes(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj")

	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, DiscoverArgs{Project: "io-foo", Regions: []string{"us-central1"}}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll called %d times, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.scope != "projects/real-proj" {
		t.Errorf("scope=%q, want projects/real-proj", c.scope)
	}
	if len(c.assetTypes) != 1 || c.assetTypes[0] != "pubsub.googleapis.com/Topic" {
		t.Errorf("assetTypes=%v, want [pubsub.googleapis.com/Topic]", c.assetTypes)
	}
	if !strings.Contains(c.query, "labels.project:io-foo") {
		t.Errorf("query=%q, want it to include labels.project:io-foo", c.query)
	}
	if !strings.Contains(c.query, "location:us-central1") {
		t.Errorf("query=%q, want it to include location:us-central1", c.query)
	}
}

func TestDiscoverTypes_DefaultsToAllSupported(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj")
	if _, err := g.DiscoverTypes(context.Background(), nil, DiscoverArgs{Project: "io-foo", Regions: []string{""}}); err != nil {
		t.Fatal(err)
	}
	// The two-bucket dispatch (#366) issues at most one SearchAll per
	// non-empty ScopeStyle bucket. When the registry covers both
	// buckets, the union of asset types across all calls must equal
	// the supported set.
	if len(fake.calls) < 1 || len(fake.calls) > 2 {
		t.Fatalf("SearchAll called %d times, want 1 or 2 (one per non-empty ScopeStyle bucket)", len(fake.calls))
	}
	covered := map[string]struct{}{}
	for _, c := range fake.calls {
		for _, at := range c.assetTypes {
			covered[at] = struct{}{}
		}
	}
	if len(covered) != len(g.SupportedTypes()) {
		t.Errorf("covered asset-types=%d (across %d calls), want %d (one per registered type)",
			len(covered), len(fake.calls), len(g.SupportedTypes()))
	}
}

func TestDiscoverTypes_EmptyProjectAndRegionEmptyQuery(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj")
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_storage_bucket"}, DiscoverArgs{Project: "", Regions: []string{""}}); err != nil {
		t.Fatal(err)
	}
	if fake.calls[0].query != "" {
		t.Errorf("query=%q, want empty when neither stack-project nor region is set", fake.calls[0].query)
	}
}

func TestDiscoverTypes_UnknownTypeAggregatesError(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "p")
	_, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "bogus", "also_bogus"}, DiscoverArgs{Project: "io-foo"})
	if err == nil {
		t.Fatal("expected error for unknown types")
	}
	if !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "also_bogus") {
		t.Errorf("error should list every unknown type; got: %v", err)
	}
}

func TestDiscoverTypes_PropagatesSearcherError(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{err: errors.New("PermissionDenied")}, "p")
	_, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, DiscoverArgs{Project: "io-foo", Regions: []string{""}})
	if err == nil || !strings.Contains(err.Error(), "PermissionDenied") {
		t.Errorf("err=%v, want wrap of PermissionDenied", err)
	}
}

func TestDiscoverTypes_TranslatesAndSortsByName(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/zeta", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
			{Name: "//storage.googleapis.com/io-foo-bucket", AssetType: "storage.googleapis.com/Bucket", Project: "real-proj", Location: "us-central1"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj")
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_storage_bucket"}, DiscoverArgs{Project: "io-foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	// Topics come first because the discoverer ordering follows the
	// `selected` slice in DiscoverTypes (insertion order). Within each
	// type, results are sorted by Cloud Asset Name.
	if got[0].Identity.Type != "google_pubsub_topic" || got[0].Identity.NameHint != "alpha" {
		t.Errorf("got[0] = %s/%s, want google_pubsub_topic/alpha (sorted within type)", got[0].Identity.Type, got[0].Identity.NameHint)
	}
	if got[1].Identity.Type != "google_pubsub_topic" || got[1].Identity.NameHint != "zeta" {
		t.Errorf("got[1] = %s/%s, want google_pubsub_topic/zeta", got[1].Identity.Type, got[1].Identity.NameHint)
	}
	if got[2].Identity.Type != "google_storage_bucket" || got[2].Identity.NameHint != "io-foo-bucket" {
		t.Errorf("got[2] = %s/%s, want google_storage_bucket/io-foo-bucket", got[2].Identity.Type, got[2].Identity.NameHint)
	}
	if got[2].Identity.Location != "us-central1" {
		t.Errorf("storage bucket Location=%q, want us-central1 (from asset)", got[2].Identity.Location)
	}
	for _, r := range got {
		if r.Identity.Cloud != "gcp" {
			t.Errorf("Cloud=%q, want gcp", r.Identity.Cloud)
		}
		if r.Identity.ProjectID != "real-proj" {
			t.Errorf("ProjectID=%q, want real-proj", r.Identity.ProjectID)
		}
		if r.Identity.ProviderConfig != gcpProviderConfigAlias {
			t.Errorf("ProviderConfig=%q, want %q", r.Identity.ProviderConfig, gcpProviderConfigAlias)
		}
		if r.Identity.ProviderSource != gcpProviderSource {
			t.Errorf("ProviderSource=%q, want %q", r.Identity.ProviderSource, gcpProviderSource)
		}
		if r.Tier != imported.TierImportedFlat {
			t.Errorf("Tier=%q, want TierImportedFlat", r.Tier)
		}
		if r.Source != imported.SourceImporter {
			t.Errorf("Source=%q, want SourceImporter", r.Source)
		}
	}
}

func TestDiscoverTypes_SkipsAssetsForUnsupportedTypes(t *testing.T) {
	t.Parallel()
	// The Cloud Asset response can carry types we didn't ask for if the
	// caller routes our query through a multi-tenant proxy that ignores
	// AssetTypes. Defense-in-depth: drop them rather than panicking on
	// a missing per-type discoverer.
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic"},
			{Name: "//unsupported.googleapis.com/projects/real-proj/things/x", AssetType: "unsupported.googleapis.com/Thing"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj")
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, DiscoverArgs{Project: "io-foo", Regions: []string{""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Identity.Type != "google_pubsub_topic" {
		t.Errorf("got=%v, want exactly one google_pubsub_topic", got)
	}
}

func TestDiscoverByID_DispatchesAndPropagatesErrNotSupported(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "real-proj")

	got, err := g.DiscoverByID(context.Background(), "google_pubsub_topic", "projects/real-proj/topics/alpha", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "google_pubsub_topic" || got.Identity.NameHint != "alpha" {
		t.Errorf("DiscoverByID returned %s/%s, want google_pubsub_topic/alpha", got.Identity.Type, got.Identity.NameHint)
	}
	if got.Identity.ProjectID != "real-proj" {
		t.Errorf("ProjectID=%q, want real-proj (from constructor when accountID empty)", got.Identity.ProjectID)
	}

	_, err = g.DiscoverByID(context.Background(), "google_brand_new", "x", "", "")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("err=%v, want ErrNotSupported for unregistered type", err)
	}
}

func TestDiscoverByID_AccountIDOverridesConstructorProject(t *testing.T) {
	t.Parallel()
	// The orchestrator passes accountID through the discoveryAggregator
	// interface; for GCP the slot carries the real project ID. When an
	// override is supplied, honor it rather than the constructor value
	// — matches the AWS path where accountID flows through.
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "ctor-proj")
	got, err := g.DiscoverByID(context.Background(), "google_pubsub_topic", "alpha", "", "override-proj")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ProjectID != "override-proj" {
		t.Errorf("ProjectID=%q, want override-proj (accountID slot)", got.Identity.ProjectID)
	}
}

// TestFromAsset_ProjectIDArgWinsOverAssetField pins that every per-type
// FromAsset constructs ImportID + Identity.ProjectID from the explicit
// `projectID` argument, NOT from gcpAssetResult.Project. A mutation that
// read `a.Project` would survive the happy-path tests (where the test
// fixtures pass the same value in both slots by convention) — this
// asserts the contract by setting them to distinct values. Covers all
// 5 Phase-1 types in one pass to catch the same regression class on
// any discoverer.
func TestFromAsset_ProjectIDArgWinsOverAssetField(t *testing.T) {
	t.Parallel()
	const explicit = "explicit-arg-proj"
	const fromAsset = "asset-claims-proj"

	cases := []struct {
		name         string
		discoverer   Discoverer
		assetName    string
		wantImportID string
	}{
		{name: "pubsub_topic", discoverer: newPubsubTopicDiscoverer(),
			assetName:    "//pubsub.googleapis.com/projects/" + fromAsset + "/topics/alpha",
			wantImportID: "projects/" + explicit + "/topics/alpha"},
		{name: "pubsub_subscription", discoverer: newPubsubSubscriptionDiscoverer(),
			assetName:    "//pubsub.googleapis.com/projects/" + fromAsset + "/subscriptions/alpha",
			wantImportID: "projects/" + explicit + "/subscriptions/alpha"},
		{name: "secret_manager_secret", discoverer: newSecretManagerSecretDiscoverer(),
			assetName:    "//secretmanager.googleapis.com/projects/" + fromAsset + "/secrets/alpha",
			wantImportID: "projects/" + explicit + "/secrets/alpha"},
		{name: "compute_network", discoverer: newComputeNetworkDiscoverer(),
			assetName:    "//compute.googleapis.com/projects/" + fromAsset + "/global/networks/vpc-main",
			wantImportID: "projects/" + explicit + "/global/networks/vpc-main"},
		// Storage bucket has a bare-name ImportID (no project qualifier),
		// but Identity.ProjectID still must come from the explicit arg.
		{name: "storage_bucket", discoverer: newStorageBucketDiscoverer(),
			assetName:    "//storage.googleapis.com/io-bucket-" + fromAsset,
			wantImportID: "io-bucket-" + fromAsset},
		{name: "service_account", discoverer: newServiceAccountDiscoverer(),
			assetName:    "//iam.googleapis.com/projects/" + fromAsset + "/serviceAccounts/sa@x.iam.gserviceaccount.com",
			wantImportID: "projects/" + explicit + "/serviceAccounts/sa@x.iam.gserviceaccount.com"},
		{name: "kms_key_ring", discoverer: newKMSKeyRingDiscoverer(),
			assetName:    "//cloudkms.googleapis.com/projects/" + fromAsset + "/locations/global/keyRings/ring1",
			wantImportID: "projects/" + explicit + "/locations/global/keyRings/ring1"},
		{name: "kms_crypto_key", discoverer: newKMSCryptoKeyDiscoverer(),
			assetName:    "//cloudkms.googleapis.com/projects/" + fromAsset + "/locations/global/keyRings/ring1/cryptoKeys/key1",
			wantImportID: "projects/" + explicit + "/locations/global/keyRings/ring1/cryptoKeys/key1"},
		{name: "compute_firewall", discoverer: newComputeFirewallDiscoverer(),
			assetName:    "//compute.googleapis.com/projects/" + fromAsset + "/global/firewalls/fw1",
			wantImportID: "projects/" + explicit + "/global/firewalls/fw1"},
		{name: "compute_router", discoverer: newComputeRouterDiscoverer(),
			assetName:    "//compute.googleapis.com/projects/" + fromAsset + "/regions/us-central1/routers/r1",
			wantImportID: "projects/" + explicit + "/regions/us-central1/routers/r1"},
		{name: "compute_address_regional", discoverer: newComputeAddressDiscoverer(),
			assetName:    "//compute.googleapis.com/projects/" + fromAsset + "/regions/us-central1/addresses/ip1",
			wantImportID: "projects/" + explicit + "/regions/us-central1/addresses/ip1"},
		{name: "compute_instance", discoverer: newComputeInstanceDiscoverer(),
			assetName:    "//compute.googleapis.com/projects/" + fromAsset + "/zones/us-central1-a/instances/vm1",
			wantImportID: "projects/" + explicit + "/zones/us-central1-a/instances/vm1"},
		{name: "container_cluster", discoverer: newContainerClusterDiscoverer(),
			assetName:    "//container.googleapis.com/projects/" + fromAsset + "/locations/us-central1/clusters/c1",
			wantImportID: "projects/" + explicit + "/locations/us-central1/clusters/c1"},
		{name: "container_node_pool", discoverer: newContainerNodePoolDiscoverer(),
			assetName:    "//container.googleapis.com/projects/" + fromAsset + "/locations/us-central1/clusters/c1/nodePools/np1",
			wantImportID: "projects/" + explicit + "/locations/us-central1/clusters/c1/nodePools/np1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.discoverer.FromAsset(addressBook{},
				gcpAssetResult{
					Name:      tc.assetName,
					AssetType: tc.discoverer.AssetType(),
					Project:   fromAsset, // intentionally != explicit
				},
				explicit)
			if got.Identity.ProjectID != explicit {
				t.Errorf("ProjectID=%q, want %q (the explicit arg, not gcpAssetResult.Project)", got.Identity.ProjectID, explicit)
			}
			if got.Identity.ImportID != tc.wantImportID {
				t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, tc.wantImportID)
			}
		})
	}
}

// TestBuildSearchQuery_Composition pins the AND-join, the kept-empty
// branches, the multi-region OR-clause shape (#291), and operator-
// supplied tag-selector clauses. A mutation that emitted
// `labels.project = io-foo` (with `=` instead of `:`) or that
// included the empty terms anyway would produce invalid Cloud Asset
// queries; the literal-string assertions catch both shapes.
func TestBuildSearchQuery_Composition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		project   string
		locations []string
		selectors []TagSelector
		want      string
	}{
		// Pre-#291 baseline cases (no selectors, single or zero region).
		{name: "both project and single region", project: "io-foo", locations: []string{"us-east1"}, want: "labels.project:io-foo AND location:us-east1"},
		{name: "project only", project: "io-foo", want: "labels.project:io-foo"},
		{name: "single region only", locations: []string{"us-east1"}, want: "location:us-east1"},
		{name: "neither", want: ""},

		// Empty-string-filtering cases (#291). The natural shape from
		// a no-default GCP path is a single-element slice containing
		// the empty string; that must NOT produce the invalid
		// "location:" clause. Mixed empty + non-empty must fall
		// through to the single-region branch (one cleaned location).
		{name: "single empty location stripped", locations: []string{""}, want: ""},
		{name: "mixed empty + valid falls through to single", locations: []string{"", "us-east1"}, want: "location:us-east1"},

		// #291 multi-region cases. Two-or-more locations emit a
		// parenthesized `(location:l1 OR location:l2 OR ...)` clause —
		// parens are non-optional because Cloud Asset's implicit `AND`
		// binds tighter than `OR`.
		{
			name:      "multi-region only",
			locations: []string{"us-east1", "us-central1"},
			want:      "(location:us-east1 OR location:us-central1)",
		},
		{
			name:      "project and multi-region",
			project:   "io-foo",
			locations: []string{"us-east1", "eu-west1"},
			want:      "labels.project:io-foo AND (location:us-east1 OR location:eu-west1)",
		},
		{
			name:      "multi-region OR ordering pin",
			locations: []string{"a", "b", "c"},
			want:      "(location:a OR location:b OR location:c)",
		},

		// #291 selector cases.
		{
			name:      "selector only",
			selectors: []TagSelector{{Key: "env", Value: "prod"}},
			want:      "labels.env:prod",
		},
		{
			name:      "project and selector",
			project:   "io-foo",
			selectors: []TagSelector{{Key: "env", Value: "prod"}},
			want:      "labels.project:io-foo AND labels.env:prod",
		},
		{
			name:      "multi-region and selector",
			project:   "io-foo",
			locations: []string{"us-east1", "us-central1"},
			selectors: []TagSelector{{Key: "env", Value: "prod"}},
			want:      "labels.project:io-foo AND (location:us-east1 OR location:us-central1) AND labels.env:prod",
		},
		{
			name:      "multi-selector ordering pin",
			project:   "io-foo",
			selectors: []TagSelector{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}},
			want:      "labels.project:io-foo AND labels.env:prod AND labels.team:growth",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := buildSearchQuery(tc.project, tc.locations, tc.selectors); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRegistryParity_GCP_LiveMatchesRegistry guards against drift between
// this package's live constructor map and the public list in
// pkg/insideout-import/registry. If a new type is registered here without
// updating the registry (or vice versa), the reliable-side wizard will
// silently disagree with what the CLI actually supports — this test fails
// first instead.
//
// Note this only pins drift between the two sources of truth. Literal-value
// pinning (the contract reliable consumers depend on) lives in the registry
// package's own tests; we don't reach across the import boundary to assert
// it twice.
func TestRegistryParity_GCP_LiveMatchesRegistry(t *testing.T) {
	t.Parallel()
	live := NewGCPDiscoverer(&fakeAssetSearcher{}, "p").SupportedTypes()
	if len(live) == 0 {
		t.Fatal("gcpdiscover registered no types — registry parity check would be tautologically empty")
	}
	pub := registry.SupportedDiscoverTypes(registry.ProviderGCP)
	if !reflect.DeepEqual(live, pub) {
		t.Errorf("registry drift: gcpdiscover=%v, registry=%v", live, pub)
	}
}

// TestGCPDiscoverTypes_EmitsServiceStartFinish_OnceForCloudAssetInventory
// (#295) pins the GCP-side contract: Cloud Asset is a single discovery
// surface (one SearchAllResources call covers every asset type for a
// project) so we emit one service_start + one service_finish per run,
// regardless of how many resource types or regions were requested. The
// service slug is "cloud_asset_inventory" — the API's product name —
// and matches what the reliable agent-API SSE translator routes on.
func TestGCPDiscoverTypes_EmitsServiceStartFinish_OnceForCloudAssetInventory(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj")
	rec := &recordingEmitter{}
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_storage_bucket"}, DiscoverArgs{
		Project: "io-foo",
		Regions: []string{"us-central1", "europe-west1"},
		Emitter: rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := 0
	finishes := 0
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			starts++
			if e.Service != "cloud_asset_inventory" {
				t.Errorf("service_start.service=%q, want cloud_asset_inventory", e.Service)
			}
		case "service_finish":
			finishes++
			if e.Service != "cloud_asset_inventory" {
				t.Errorf("service_finish.service=%q, want cloud_asset_inventory", e.Service)
			}
		}
	}
	if starts != 1 {
		t.Errorf("service_start count=%d, want 1 (Cloud Asset is single-call)", starts)
	}
	if finishes != 1 {
		t.Errorf("service_finish count=%d, want 1", finishes)
	}
}

// TestGCPDiscoverTypes_EmitsItemFound_PerAsset (#295) pins one
// item_found per emitted ImportedResource, with the asset's Location
// stamped into the event so consumers can attribute multi-region
// scans correctly. The TF type matches the per-discoverer
// ResourceType().
func TestGCPDiscoverTypes_EmitsItemFound_PerAsset(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/alpha", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/zeta", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
			{Name: "//storage.googleapis.com/io-foo-bucket", AssetType: "storage.googleapis.com/Bucket", Project: "real-proj", Location: "us-central1"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj")
	rec := &recordingEmitter{}
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_storage_bucket"}, DiscoverArgs{
		Project: "io-foo",
		Emitter: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "item_found" {
			items = append(items, e)
		}
	}
	if len(items) != len(got) {
		t.Errorf("item_found count=%d, want %d (one per emitted resource)", len(items), len(got))
	}
	// At least one item should carry a non-empty Location (the storage
	// bucket); pubsub topics are project-global and may have empty
	// Location on the asset.
	sawLoc := false
	for _, it := range items {
		if it.Service != "cloud_asset_inventory" {
			t.Errorf("item.service=%q, want cloud_asset_inventory", it.Service)
		}
		if it.TFType == "" {
			t.Errorf("item.tf_type empty: %+v", it)
		}
		if it.ImportID == "" {
			t.Errorf("item.import_id empty: %+v", it)
		}
		if it.Region == "us-central1" {
			sawLoc = true
		}
	}
	if !sawLoc {
		t.Errorf("expected at least one item_found with region=us-central1; got %d items", len(items))
	}
}

// TestGCPDiscoverTypes_EmitsStageFinish (#295) pins that DiscoverTypes
// closes the stage with one stage_finish event whose total matches the
// emitted resource count. The orchestrator-level test in the main
// package asserts the same property at the runDiscoverWithDeps boundary;
// this test pins it inside the aggregator so a regression that moves
// the StageFinish out of DiscoverTypes (e.g. into the per-type loop)
// surfaces here first.
func TestGCPDiscoverTypes_EmitsStageFinish(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//pubsub.googleapis.com/projects/real-proj/topics/a", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj")
	rec := &recordingEmitter{}
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, DiscoverArgs{
		Project: "io-foo",
		Emitter: rec,
	}); err != nil {
		t.Fatal(err)
	}
	stageEvents := 0
	for _, e := range rec.snapshot() {
		if e.Kind == "stage_finish" {
			stageEvents++
			if e.Stage != "discover" {
				t.Errorf("stage_finish.stage=%q, want discover", e.Stage)
			}
			if e.Total != 1 {
				t.Errorf("stage_finish.total=%d, want 1", e.Total)
			}
		}
	}
	if stageEvents != 1 {
		t.Errorf("stage_finish count=%d, want 1", stageEvents)
	}
}

// expectedScopeStyle is the per-type ScopeStyle contract (#366) —
// adding a new discoverer to NewGCPDiscoverer requires an explicit
// row here, and TestScopeStyle_PinsPerTypeContract fails on drift in
// either direction (unknown registered type, or known type with a
// different ScopeStyle). Adding a row is an intentional decision: a
// type that carries GCP labels should be ScopeStyleLabels; a label-
// less type (CLAUDE.md L84 convention) should be ScopeStyleNamePrefix.
var expectedScopeStyle = map[string]ScopeStyle{
	"google_pubsub_topic":          ScopeStyleLabels,
	"google_pubsub_subscription":   ScopeStyleLabels,
	"google_storage_bucket":        ScopeStyleLabels,
	"google_secret_manager_secret": ScopeStyleLabels,
	"google_compute_network":       ScopeStyleLabels,
	"google_service_account":       ScopeStyleNamePrefix, // IAM SAs have no labels (#367)
	"google_kms_key_ring":          ScopeStyleNamePrefix, // KMS keyrings have no labels (#368)
	"google_kms_crypto_key":        ScopeStyleNamePrefix, // KMS cryptokeys have no labels (#368)
	"google_compute_firewall":      ScopeStyleNamePrefix, // firewalls have no labels (#369)
	"google_compute_router":        ScopeStyleNamePrefix, // routers have no labels (#369)
	"google_compute_address":       ScopeStyleLabels,     // addresses carry labels (#369)
	"google_compute_instance":      ScopeStyleLabels,     // VMs carry labels (#370)
	"google_container_cluster":     ScopeStyleLabels,     // GKE clusters carry labels (#371)
	"google_container_node_pool":   ScopeStyleNamePrefix, // node pools have no labels (#371)
}

// TestScopeStyle_PinsPerTypeContract is the regression guard (#366).
// Every registered discoverer must have a row in expectedScopeStyle
// asserting the right scoping. A mutation that flipped a label-
// carrying type to NamePrefix would silently widen its server-side
// scope; a mutation that flipped a label-less type to Labels would
// silently drop every result (the labels.project clause returns zero
// rows for label-less resource types).
//
// Test source-of-truth is the live registration map produced by
// NewGCPDiscoverer, joined against the hand-maintained
// expectedScopeStyle table. New types added to NewGCPDiscoverer must
// also be added to the table — surfaces both forms of drift here.
func TestScopeStyle_PinsPerTypeContract(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "p")
	if len(g.byType) == 0 {
		t.Fatal("no discoverers registered; ScopeStyle parity test would be vacuous")
	}
	for tfType, d := range g.byType {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			want, ok := expectedScopeStyle[tfType]
			if !ok {
				t.Errorf("registered type %q has no expectedScopeStyle row — add an explicit entry to pin the ScopeStyle contract", tfType)
				return
			}
			if got := d.ScopeStyle(); got != want {
				t.Errorf("%s.ScopeStyle()=%v, want %v", tfType, got, want)
			}
		})
	}
	for tfType := range expectedScopeStyle {
		if _, ok := g.byType[tfType]; !ok {
			t.Errorf("expectedScopeStyle row for %q has no live registration — remove the row or register the type", tfType)
		}
	}
}

// TestDiscoverTypes_LabelsBucketOnly_SingleSearchCall pins today's
// labels-only path (#366) — when every selected discoverer reports
// ScopeStyleLabels, the orchestrator issues exactly one
// SearchAllResources call and its query carries the legacy
// `labels.project:<stack>` clause. Regression guard against the
// two-bucket refactor accidentally splitting the all-labels path into
// two round-trips.
func TestDiscoverTypes_LabelsBucketOnly_SingleSearchCall(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj")
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_storage_bucket"}, DiscoverArgs{
		Project: "io-foo",
		Regions: []string{""},
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll calls=%d, want 1 (all selected types are ScopeStyleLabels)", len(fake.calls))
	}
	if !strings.Contains(fake.calls[0].query, "labels.project:io-foo") {
		t.Errorf("labels-bucket query=%q, want it to include labels.project:io-foo", fake.calls[0].query)
	}
}

// TestDiscoverTypes_NamePrefixBucketOnly_OneCallWithoutLabelsClause
// pins the name-prefix-only path (#366): one SearchAllResources call,
// and its query MUST NOT include `labels.project:` — label-less GCP
// resource types don't carry the label, so the server-side clause
// would unconditionally exclude every result.
func TestDiscoverTypes_NamePrefixBucketOnly_OneCallWithoutLabelsClause(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_fake_namepref": &fakeNamePrefixDiscoverer{
				resourceType: "google_fake_namepref",
				assetType:    "test.googleapis.com/NamePrefixed",
			},
		},
	}
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_fake_namepref"}, DiscoverArgs{
		Project: "io-foo",
		Regions: []string{"us-central1"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll calls=%d, want 1 (one bucket, one round-trip)", len(fake.calls))
	}
	if strings.Contains(fake.calls[0].query, "labels.project:") {
		t.Errorf("name-prefix-bucket query=%q, want NO labels.project clause", fake.calls[0].query)
	}
	// Sanity: the other clauses still propagate (location, selectors).
	if !strings.Contains(fake.calls[0].query, "location:us-central1") {
		t.Errorf("name-prefix-bucket query=%q, want location:us-central1 to still propagate", fake.calls[0].query)
	}
}

// TestDiscoverTypes_MixedBuckets_TwoSearchCallsWithDifferentQueries
// pins the two-call dispatch (#366) when both ScopeStyle buckets are
// non-empty: each bucket gets its own SearchAllResources, partitioned
// by asset type. The labels-bucket call carries `labels.project:`; the
// name-prefix-bucket call doesn't.
func TestDiscoverTypes_MixedBuckets_TwoSearchCallsWithDifferentQueries(t *testing.T) {
	t.Parallel()
	fake := &bucketedFakeSearcher{
		resultsByAssetType: map[string][]gcpAssetResult{
			"pubsub.googleapis.com/Topic": {
				{Name: "//pubsub.googleapis.com/projects/real-proj/topics/io-foo-events", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj"},
			},
			"test.googleapis.com/NamePrefixed": {
				{Name: "//test.googleapis.com/projects/real-proj/widgets/io-foo-widget", AssetType: "test.googleapis.com/NamePrefixed", Project: "real-proj"},
			},
		},
	}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_pubsub_topic": newPubsubTopicDiscoverer(),
			"google_fake_namepref": &fakeNamePrefixDiscoverer{
				resourceType: "google_fake_namepref",
				assetType:    "test.googleapis.com/NamePrefixed",
			},
		},
	}
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_fake_namepref"}, DiscoverArgs{
		Project: "io-foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("SearchAll calls=%d, want 2 (one per ScopeStyle bucket)", len(fake.calls))
	}

	// Find the calls by their asset-type partition; the orchestrator
	// iterates labels-bucket before name-prefix-bucket, but pinning the
	// order is brittle — pin the contents instead.
	var labelsCall, nameCall *searchAllCall
	for i := range fake.calls {
		c := &fake.calls[i]
		switch {
		case slices.Contains(c.assetTypes, "pubsub.googleapis.com/Topic"):
			labelsCall = c
		case slices.Contains(c.assetTypes, "test.googleapis.com/NamePrefixed"):
			nameCall = c
		}
	}
	if labelsCall == nil {
		t.Fatal("no SearchAll call carried the labels-bucket asset type pubsub.googleapis.com/Topic")
	}
	if nameCall == nil {
		t.Fatal("no SearchAll call carried the name-prefix-bucket asset type test.googleapis.com/NamePrefixed")
	}

	if !strings.Contains(labelsCall.query, "labels.project:io-foo") {
		t.Errorf("labels-bucket call query=%q, want labels.project:io-foo", labelsCall.query)
	}
	// Belt-and-braces: the labels-bucket query carries exactly one
	// labels.project clause. A regression that double-emitted the
	// clause (e.g. labels.project:io-foo AND labels.project:io-foo)
	// or that merged in tag selectors as labels.project would still
	// satisfy the Contains() check above.
	if c := strings.Count(labelsCall.query, "labels.project:"); c != 1 {
		t.Errorf("labels-bucket labels.project count=%d, want 1; query=%q", c, labelsCall.query)
	}
	if strings.Contains(nameCall.query, "labels.project:") {
		t.Errorf("name-prefix-bucket call query=%q, want NO labels.project clause", nameCall.query)
	}

	// Asset-type partition is strict: neither call carries the other
	// bucket's type. A regression that issued one combined call would
	// fail here.
	if slices.Contains(labelsCall.assetTypes, "test.googleapis.com/NamePrefixed") {
		t.Errorf("labels-bucket call asset types=%v, must not include name-prefix-bucket types", labelsCall.assetTypes)
	}
	if slices.Contains(nameCall.assetTypes, "pubsub.googleapis.com/Topic") {
		t.Errorf("name-prefix-bucket call asset types=%v, must not include labels-bucket types", nameCall.assetTypes)
	}

	// Both buckets contribute to the returned resource set.
	if len(got) != 2 {
		t.Fatalf("ImportedResources=%d, want 2 (one per bucket)", len(got))
	}
}

// TestDiscoverTypes_NamePrefixFiltersAssetNameClientSide pins the
// client-side filter (#366): when the name-prefix bucket returns
// assets whose short name doesn't contain args.Project, the
// orchestrator drops them. The server has no `labels.project:` clause
// for this bucket — so the filter must apply on the client, otherwise
// label-less GCP resources from other stacks would leak into the
// import set.
func TestDiscoverTypes_NamePrefixFiltersAssetNameClientSide(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			// Matches "io-foo" via substring on the short name.
			{Name: "//test.googleapis.com/projects/real-proj/widgets/io-foo-widget-a", AssetType: "test.googleapis.com/NamePrefixed"},
			// Does NOT contain "io-foo" — must be dropped.
			{Name: "//test.googleapis.com/projects/real-proj/widgets/other-stack-widget", AssetType: "test.googleapis.com/NamePrefixed"},
			// Matches via substring at the end.
			{Name: "//test.googleapis.com/projects/real-proj/widgets/widget-io-foo", AssetType: "test.googleapis.com/NamePrefixed"},
		},
	}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_fake_namepref": &fakeNamePrefixDiscoverer{
				resourceType: "google_fake_namepref",
				assetType:    "test.googleapis.com/NamePrefixed",
			},
		},
	}
	got, err := g.DiscoverTypes(context.Background(), []string{"google_fake_namepref"}, DiscoverArgs{
		Project: "io-foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pin both the partition (exactly which assets survived) and the
	// order (DiscoverTypes stable-sorts per-asset-type by Cloud Asset
	// Name). A mutation that kept one asset twice or that swapped
	// asset[0]/asset[2] would survive a count-only check; deep-equal
	// against the sorted-by-short-name expected slice pins it.
	wantNames := []string{"io-foo-widget-a", "widget-io-foo"}
	gotNames := namesOf(got)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("kept names = %v, want %v (filter must keep the io-foo-tagged assets in stable order, drop other-stack-widget)", gotNames, wantNames)
	}
}

// TestDiscoverTypes_NamePrefixEmptyProjectPassesThrough pins the
// no-filter case (#366): when args.Project is empty, the name-prefix
// bucket's client-side filter is skipped so every asset passes
// through. Symmetric with the labels bucket, where an empty stack
// project also omits the labels.project clause and returns the
// project's full inventory.
//
// Mutation-resistance: strings.Contains(s, "") is always true in Go,
// so a regression that removed the `if args.Project != ""` guard
// would still produce a 2-result happy-path under any reasonable
// fixture — the count-only assertion is tautological. To pin the
// behavior, this test feeds three assets whose short names share
// nothing in common (no substring overlap) and asserts the returned
// slice exactly equals the input by name AND order. The labels-style
// `len == in` shape and unordered membership would not catch a
// stable-sort regression on the name-prefix path; deep-equal pins it.
func TestDiscoverTypes_NamePrefixEmptyProjectPassesThrough(t *testing.T) {
	t.Parallel()
	// Three assets whose short names form an antichain under
	// strings.Contains — no name is a substring of another, so any
	// substring filter run against a non-empty needle would drop at
	// least one. The skip-the-filter branch must keep all three.
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//test.googleapis.com/projects/real-proj/widgets/alpha", AssetType: "test.googleapis.com/NamePrefixed"},
			{Name: "//test.googleapis.com/projects/real-proj/widgets/zeta", AssetType: "test.googleapis.com/NamePrefixed"},
			{Name: "//test.googleapis.com/projects/real-proj/widgets/mid", AssetType: "test.googleapis.com/NamePrefixed"},
		},
	}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_fake_namepref": &fakeNamePrefixDiscoverer{
				resourceType: "google_fake_namepref",
				assetType:    "test.googleapis.com/NamePrefixed",
			},
		},
	}
	got, err := g.DiscoverTypes(context.Background(), []string{"google_fake_namepref"}, DiscoverArgs{
		Project: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	// DiscoverTypes sorts each per-asset-type bucket by Cloud Asset
	// Name (gcpdiscover.go's stable-sort), so the expected order is
	// the alphabetical sort of short names: alpha, mid, zeta.
	wantNames := []string{"alpha", "mid", "zeta"}
	gotNames := namesOf(got)
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("kept names = %v, want %v (every input must pass through unchanged when args.Project is empty)", gotNames, wantNames)
	}
}

// TestMatchesNamePrefix pins the helper's contract (#366): substring
// match against the trailing path segment of a Cloud Asset name, with
// the rest of the path explicitly excluded from the match. The
// adversarial cases (5-8) are load-bearing: they pin the property
// that earlier path segments — including the GCP project ID, the
// location, and the resource-collection name — cannot trigger a
// false-positive match. A mutation that replaced
// `strings.Contains(shortName(assetName), stackProject)` with
// `strings.Contains(assetName, stackProject)` would silently widen
// the scope to the entire stack's project, returning resources
// belonging to other stacks. The adversarial rows fail loudly.
func TestMatchesNamePrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		assetName string
		project   string
		want      bool
	}{
		// Happy paths — substring at every position of the short name.
		{"prefix match in short name", "//iam.googleapis.com/projects/p/serviceAccounts/io-foo-sa@p.iam.gserviceaccount.com", "io-foo", true},
		{"infix match in short name", "//compute.googleapis.com/projects/p/global/firewalls/fw-io-foo-allow", "io-foo", true},
		{"suffix match in short name", "//cloudkms.googleapis.com/projects/p/locations/global/keyRings/ring-io-foo", "io-foo", true},
		{"no match anywhere", "//compute.googleapis.com/projects/p/global/firewalls/other-stack-fw", "io-foo", false},

		// Adversarial: the stack project appears in earlier segments
		// (the GCP project ID, an intermediate path segment, or both)
		// while the trailing short name is OWNED BY A DIFFERENT STACK.
		// Each must return false — only short-name matches count.
		{"stack project in GCP project ID segment", "//iam.googleapis.com/projects/io-foo-real-proj/serviceAccounts/other-stack-sa@x.iam.gserviceaccount.com", "io-foo", false},
		{"stack project equals GCP project ID", "//compute.googleapis.com/projects/io-foo/global/firewalls/different-stack-fw", "io-foo", false},
		{"stack project is substring of GCP project ID", "//compute.googleapis.com/projects/my-io-foo-prod/global/firewalls/different-stack-fw", "io-foo", false},
		{"stack project in intermediate location segment", "//cloudkms.googleapis.com/projects/p/locations/io-foo-zone/keyRings/other-stack-ring", "io-foo", false},

		// Edge cases.
		{"bare name (no slashes)", "io-foo-widget", "io-foo", true},
		{"empty asset name", "", "io-foo", false},

		// Trailing slash is malformed Cloud Asset input — production
		// names never end in '/'. shortName's defensive fallback
		// returns the entire asset name; the substring match then
		// sees earlier segments and may produce false positives.
		// Pinning current behavior so a refactor that silently
		// strengthened the trailing-slash branch (e.g. returning ""
		// instead of the full name) surfaces here.
		{"trailing slash falls back to full asset name (documented surface)", "//svc.googleapis.com/projects/io-foo-real/widgets/", "io-foo", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matchesNamePrefix(tc.assetName, tc.project); got != tc.want {
				t.Errorf("matchesNamePrefix(%q, %q) = %v, want %v", tc.assetName, tc.project, got, tc.want)
			}
		})
	}
}

// TestDiscoverTypes_TwoBucketsEmitOneServiceStartFinishPair extends
// the #295 contract to the two-bucket path (#366): even when both
// ScopeStyle buckets issue independent SearchAllResources calls, the
// orchestrator emits exactly one (service_start, service_finish) pair
// around the combined operation. A regression that moved the pair
// inside searchBuckets would double the events.
func TestDiscoverTypes_TwoBucketsEmitOneServiceStartFinishPair(t *testing.T) {
	t.Parallel()
	fake := &bucketedFakeSearcher{
		resultsByAssetType: map[string][]gcpAssetResult{
			"pubsub.googleapis.com/Topic": {
				{Name: "//pubsub.googleapis.com/projects/real-proj/topics/io-foo-events", AssetType: "pubsub.googleapis.com/Topic"},
			},
			"test.googleapis.com/NamePrefixed": {
				{Name: "//test.googleapis.com/projects/real-proj/widgets/io-foo-widget", AssetType: "test.googleapis.com/NamePrefixed"},
			},
		},
	}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_pubsub_topic": newPubsubTopicDiscoverer(),
			"google_fake_namepref": &fakeNamePrefixDiscoverer{
				resourceType: "google_fake_namepref",
				assetType:    "test.googleapis.com/NamePrefixed",
			},
		},
	}
	rec := &recordingEmitter{}
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_fake_namepref"}, DiscoverArgs{
		Project: "io-foo",
		Emitter: rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts, finishes := 0, 0
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			starts++
			if e.Service != "cloud_asset_inventory" {
				t.Errorf("service_start.service=%q, want cloud_asset_inventory", e.Service)
			}
		case "service_finish":
			finishes++
			if e.Service != "cloud_asset_inventory" {
				t.Errorf("service_finish.service=%q, want cloud_asset_inventory", e.Service)
			}
		}
	}
	if starts != 1 {
		t.Errorf("service_start count=%d, want 1 (one pair across both ScopeStyle buckets)", starts)
	}
	if finishes != 1 {
		t.Errorf("service_finish count=%d, want 1 (one pair across both ScopeStyle buckets)", finishes)
	}
}

// namesOf collects NameHints from a discover result for failure-message
// readability — `got 1 names: [alpha]` beats `got 1 items`.
func namesOf(rs []imported.ImportedResource) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Identity.NameHint)
	}
	return out
}
