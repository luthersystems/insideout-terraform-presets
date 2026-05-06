package gcpdiscover

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func TestNewGCPDiscoverer_RegistersPhase1Types(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "real-proj")
	got := g.SupportedTypes()
	want := map[string]bool{
		"google_pubsub_topic":          false,
		"google_pubsub_subscription":   false,
		"google_storage_bucket":        false,
		"google_secret_manager_secret": false,
		"google_compute_network":       false,
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

	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, "io-foo", "us-central1", ""); err != nil {
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
	if _, err := g.DiscoverTypes(context.Background(), nil, "io-foo", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll called %d times, want 1", len(fake.calls))
	}
	if len(fake.calls[0].assetTypes) != len(g.SupportedTypes()) {
		t.Errorf("assetTypes len=%d, want %d (one per registered type)",
			len(fake.calls[0].assetTypes), len(g.SupportedTypes()))
	}
}

func TestDiscoverTypes_EmptyProjectAndRegionEmptyQuery(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj")
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_storage_bucket"}, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if fake.calls[0].query != "" {
		t.Errorf("query=%q, want empty when neither stack-project nor region is set", fake.calls[0].query)
	}
}

func TestDiscoverTypes_UnknownTypeAggregatesError(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "p")
	_, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "bogus", "also_bogus"}, "io-foo", "", "")
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
	_, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, "io-foo", "", "")
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
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_storage_bucket"}, "io-foo", "", "")
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
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic"}, "io-foo", "", "")
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

// TestBuildSearchQuery_Composition pins the AND-join and the kept-empty
// branches. A mutation that emitted `labels.project = io-foo` (with `=`
// instead of `:`) or that included the empty terms anyway would produce
// invalid Cloud Asset queries; this test catches both shapes.
func TestBuildSearchQuery_Composition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, project, region, want string
	}{
		{name: "both", project: "io-foo", region: "us-east1", want: "labels.project:io-foo AND location:us-east1"},
		{name: "project only", project: "io-foo", region: "", want: "labels.project:io-foo"},
		{name: "region only", project: "", region: "us-east1", want: "location:us-east1"},
		{name: "neither", project: "", region: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := buildSearchQuery(tc.project, tc.region); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
