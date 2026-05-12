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
	"google_pubsub_topic":                    false,
	"google_pubsub_subscription":             false,
	"google_storage_bucket":                  false,
	"google_secret_manager_secret":           false,
	"google_compute_network":                 false,
	"google_service_account":                 false,
	"google_kms_key_ring":                    false,
	"google_kms_crypto_key":                  false,
	"google_compute_firewall":                false,
	"google_compute_router":                  false,
	"google_compute_address":                 false,
	"google_compute_global_address":          false,
	"google_compute_instance":                false,
	"google_container_cluster":               false,
	"google_container_node_pool":             false,
	"google_sql_database_instance":           false,
	"google_cloud_run_v2_service":            false,
	"google_cloudfunctions2_function":        false,
	"google_compute_forwarding_rule":         false,
	"google_compute_global_forwarding_rule":  false,
	"google_compute_target_https_proxy":      false,
	"google_compute_url_map":                 false,
	"google_api_gateway_api":                 false,
	"google_api_gateway_api_config":          false,
	"google_api_gateway_gateway":             false,
	"google_monitoring_dashboard":            false,
	"google_monitoring_alert_policy":         false,
	"google_monitoring_notification_channel": false,
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
	// The N-bucket dispatch (#366, #381) issues one SearchAll per
	// non-empty ScopeStyle bucket. The production registry currently
	// populates all three buckets (labels + name-prefix + parent-
	// name-prefix), so the production call count is exactly 3. A
	// regression that collapsed two buckets into one would silently
	// pass a `1-3` range assertion, so pin the exact value. If a
	// future change empties a ScopeStyle bucket out of the registry
	// (e.g. flips the last NamePrefix type to Labels), update this
	// expected count in lockstep.
	const wantCalls = 3
	if len(fake.calls) != wantCalls {
		t.Fatalf("SearchAll called %d times, want %d (one per non-empty ScopeStyle bucket)", len(fake.calls), wantCalls)
	}
	// Collect the distinct Cloud Asset asset-types requested across
	// all calls. The count must equal the number of DISTINCT asset
	// types in the registry — strictly less than the number of
	// registered TF types when two discoverers share a CAI slug
	// (regional + global address share compute.googleapis.com/Address,
	// regional + global forwarding rule share
	// compute.googleapis.com/ForwardingRule; see #384). Compute the
	// expected from the registry itself rather than hardcoding so
	// future shared-slug pairs don't drift this assertion.
	covered := map[string]struct{}{}
	for _, c := range fake.calls {
		for _, at := range c.assetTypes {
			covered[at] = struct{}{}
		}
	}
	wantDistinctAssetTypes := map[string]struct{}{}
	for _, d := range g.byType {
		wantDistinctAssetTypes[d.AssetType()] = struct{}{}
	}
	if len(covered) != len(wantDistinctAssetTypes) {
		t.Errorf("covered asset-types=%d (across %d calls), want %d (one per DISTINCT asset type — shared-CAI-slug pairs count once)",
			len(covered), len(fake.calls), len(wantDistinctAssetTypes))
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
		{name: "sql_database_instance", discoverer: newSQLDatabaseInstanceDiscoverer(),
			assetName:    "//sqladmin.googleapis.com/projects/" + fromAsset + "/instances/db1",
			wantImportID: "projects/" + explicit + "/instances/db1"},
		{name: "cloud_run_v2_service", discoverer: newCloudRunV2ServiceDiscoverer(),
			assetName:    "//run.googleapis.com/projects/" + fromAsset + "/locations/us-central1/services/s1",
			wantImportID: "projects/" + explicit + "/locations/us-central1/services/s1"},
		{name: "cloudfunctions2_function", discoverer: newCloudFunctions2FunctionDiscoverer(),
			assetName:    "//cloudfunctions.googleapis.com/projects/" + fromAsset + "/locations/us-central1/functions/fn1",
			wantImportID: "projects/" + explicit + "/locations/us-central1/functions/fn1"},
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

// expectedScopeStyle is the per-type ScopeStyle contract (#366, #381)
// — adding a new discoverer to NewGCPDiscoverer requires an explicit
// row here, and TestScopeStyle_PinsPerTypeContract fails on drift in
// either direction (unknown registered type, or known type with a
// different ScopeStyle). Adding a row is an intentional decision:
//
//   - ScopeStyleLabels — type carries GCP labels.
//   - ScopeStyleNamePrefix — type is label-less and the stack project
//     is embedded in the resource's own short name (CLAUDE.md L84).
//   - ScopeStyleParentNamePrefix — type is label-less and child to a
//     parent whose name embeds the stack project (e.g. KMS cryptokey
//     under keyring, GKE node pool under cluster) (#381).
var expectedScopeStyle = map[string]ScopeStyle{
	"google_pubsub_topic":                    ScopeStyleLabels,
	"google_pubsub_subscription":             ScopeStyleLabels,
	"google_storage_bucket":                  ScopeStyleLabels,
	"google_secret_manager_secret":           ScopeStyleLabels,
	"google_compute_network":                 ScopeStyleLabels,
	"google_service_account":                 ScopeStyleNamePrefix,       // IAM SAs have no labels (#367)
	"google_kms_key_ring":                    ScopeStyleNamePrefix,       // KMS keyrings have no labels (#368)
	"google_kms_crypto_key":                  ScopeStyleParentNamePrefix, // child of keyring (#381)
	"google_compute_firewall":                ScopeStyleNamePrefix,       // firewalls have no labels (#369)
	"google_compute_router":                  ScopeStyleNamePrefix,       // routers have no labels (#369)
	"google_compute_address":                 ScopeStyleLabels,           // addresses carry labels (#369)
	"google_compute_global_address":          ScopeStyleLabels,           // global addresses carry labels (#384)
	"google_compute_instance":                ScopeStyleLabels,           // VMs carry labels (#370)
	"google_container_cluster":               ScopeStyleLabels,           // GKE clusters carry labels (#371)
	"google_container_node_pool":             ScopeStyleParentNamePrefix, // child of cluster (#381)
	"google_sql_database_instance":           ScopeStyleLabels,     // Cloud SQL via settings.user_labels (#372)
	"google_cloud_run_v2_service":            ScopeStyleLabels,     // Cloud Run v2 carries labels (#373)
	"google_cloudfunctions2_function":        ScopeStyleLabels,     // Cloud Functions v2 carries labels (#373)
	"google_compute_forwarding_rule":         ScopeStyleLabels,     // forwarding rules carry labels (#375)
	"google_compute_global_forwarding_rule":  ScopeStyleLabels,     // global forwarding rules carry labels (#384)
	"google_compute_target_https_proxy":      ScopeStyleNamePrefix, // target HTTPS proxies have no labels (#375)
	"google_compute_url_map":                 ScopeStyleNamePrefix, // URL maps have no labels (#375)
	"google_api_gateway_api":                 ScopeStyleLabels,     // API Gateway APIs carry labels (#376)
	"google_api_gateway_api_config":          ScopeStyleLabels,     // API Gateway API configs carry labels (#376)
	"google_api_gateway_gateway":             ScopeStyleLabels,     // API Gateway gateways carry labels (#376)
	"google_monitoring_dashboard":            ScopeStyleNamePrefix, // dashboards have no labels (#377)
	"google_monitoring_alert_policy":         ScopeStyleNamePrefix, // alert policies have no labels (#377)
	"google_monitoring_notification_channel": ScopeStyleNamePrefix, // notification channels have no labels (#377)
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

// TestAssetTypesOf_DedupsSharedSlug_PreservesFirstAppearanceOrder
// pins the dedup contract added in #384. Two discoverers register the
// same compute.googleapis.com/Address slug; the helper must return a
// single-entry slice in first-appearance order. Order is observable
// downstream (unit tests pin per-bucket asset-type partitioning), so
// the contract is "dedup AND preserve order", not just "dedup".
func TestAssetTypesOf_DedupsSharedSlug_PreservesFirstAppearanceOrder(t *testing.T) {
	t.Parallel()
	ds := []Discoverer{
		newComputeAddressDiscoverer(),
		newComputeGlobalAddressDiscoverer(),
		newPubsubTopicDiscoverer(),
		newComputeForwardingRuleDiscoverer(),
		newComputeGlobalForwardingRuleDiscoverer(),
	}
	got := assetTypesOf(ds)
	want := []string{
		"compute.googleapis.com/Address",        // from regional address (first appearance)
		"pubsub.googleapis.com/Topic",           // distinct slug
		"compute.googleapis.com/ForwardingRule", // from regional fwd rule (first appearance)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assetTypesOf = %v, want %v (dedup + first-appearance order)", got, want)
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

// TestDiscoverTypes_OrchestratorSkipsZeroIdentityType pins the
// orchestrator half of the skip-sentinel contract introduced by the
// Bundle 8 live-smoke fixup: a discoverer that returns a zero
// ImportedResource (empty Identity.Type) signals the orchestrator to
// drop that row from the emitted slice.
//
// Without this test, the live-smoke bug (malformed
// `projects/<p>/regions/global/addresses/<n>` ImportIDs for global
// rows) regresses if anyone deletes the
// `if imp.Identity.Type == "" { continue }` guard in
// gcpdiscover.go::DiscoverTypes — the producer-side
// TestComputeAddressFromAsset_Global_IsSkipped only proves
// FromAsset returns zero; the orchestrator skip is what actually
// drops the row.
//
// Uses a fake discoverer that returns zero for one specific asset
// name. Avoids depending on compute_address's specific global-skip
// logic so the test stays focused on the contract.
func TestDiscoverTypes_OrchestratorSkipsZeroIdentityType(t *testing.T) {
	t.Parallel()
	const tfType = "google_test_zerosignal"
	const assetType = "test.googleapis.com/ZeroSignal"
	const skipMe = "//test.googleapis.com/projects/real-proj/things/skip-me"
	const keepMe = "//test.googleapis.com/projects/real-proj/things/keep-me"

	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: skipMe, AssetType: assetType, Project: "real-proj"},
			{Name: keepMe, AssetType: assetType, Project: "real-proj"},
		},
	}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			tfType: &skipOnNameDiscoverer{
				resourceType:  tfType,
				assetType:     assetType,
				skipAssetName: skipMe,
			},
		},
	}
	got, err := g.DiscoverTypes(context.Background(), []string{tfType}, DiscoverArgs{Project: ""})
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one resource emitted (keep-me); the zero-Identity skip-me
	// row was dropped by the orchestrator.
	if len(got) != 1 {
		t.Fatalf("len(got)=%d, want 1 (zero-Identity row must be dropped); got %v", len(got), namesOf(got))
	}
	if got[0].Identity.NameHint != "keep-me" {
		t.Errorf("kept=%q, want keep-me — wrong row survived?", got[0].Identity.NameHint)
	}
}

// skipOnNameDiscoverer is a test fake whose FromAsset returns a zero
// ImportedResource when the asset.Name matches `skipAssetName`, and a
// real ImportedResource otherwise. Used by the
// TestDiscoverTypes_OrchestratorSkipsZeroIdentityType regression
// guard above.
type skipOnNameDiscoverer struct {
	resourceType  string
	assetType     string
	skipAssetName string
}

func (d *skipOnNameDiscoverer) ResourceType() string   { return d.resourceType }
func (d *skipOnNameDiscoverer) AssetType() string      { return d.assetType }
func (d *skipOnNameDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (d *skipOnNameDiscoverer) FromAsset(_ addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	if a.Name == d.skipAssetName {
		return imported.ImportedResource{}
	}
	name := shortName(a.Name)
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:     "gcp",
			Type:      d.resourceType,
			Address:   d.resourceType + "." + name,
			ImportID:  name,
			NameHint:  name,
			ProjectID: projectID,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
}

func (d *skipOnNameDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, ErrNotSupported
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

// TestParentScopedDiscoverer_ContractMatchesScopeStyle pins the
// bidirectional invariant between ScopeStyle() and the
// parentScopedDiscoverer side-interface (#381):
//
//   - Every registered discoverer that reports
//     ScopeStyleParentNamePrefix MUST implement parentScopedDiscoverer
//     and return a non-empty ParentMarker. Otherwise the orchestrator's
//     third-bucket post-filter has no marker to extract, and the
//     searchBuckets path fails loud at search time — but in production,
//     not in tests. This test surfaces it at compile-time-ish (test-time
//     against the live registration map).
//
//   - No registered discoverer with a NON-ScopeStyleParentNamePrefix
//     style may implement parentScopedDiscoverer. An accidental
//     ParentMarker on (say) a ScopeStyleNamePrefix type signals
//     contract drift — the marker won't be consulted and the silent
//     dead-code is a maintenance trap.
func TestParentScopedDiscoverer_ContractMatchesScopeStyle(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(&fakeAssetSearcher{}, "p")
	if len(g.byType) == 0 {
		t.Fatal("no discoverers registered; parent-scoped contract test would be vacuous")
	}
	for tfType, d := range g.byType {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			ps, isParent := d.(parentScopedDiscoverer)
			switch d.ScopeStyle() {
			case ScopeStyleParentNamePrefix:
				if !isParent {
					t.Errorf("%s.ScopeStyle()=ScopeStyleParentNamePrefix but does not implement parentScopedDiscoverer (add ParentMarker() string)", tfType)
					return
				}
				if marker := ps.ParentMarker(); marker == "" {
					t.Errorf("%s.ParentMarker() returned empty string; must be a non-empty path marker like \"/keyRings/\"", tfType)
				}
			default:
				if isParent {
					t.Errorf("%s implements parentScopedDiscoverer but ScopeStyle()=%v (not ScopeStyleParentNamePrefix) — accidental marker is dead code", tfType, d.ScopeStyle())
				}
			}
		})
	}
}

// TestDiscoverTypes_ParentBucketOnly_OneCallWithoutLabelsClause is the
// parent-bucket counterpart of
// TestDiscoverTypes_NamePrefixBucketOnly_OneCallWithoutLabelsClause
// (#381). When every selected discoverer reports
// ScopeStyleParentNamePrefix, the orchestrator issues exactly one
// SearchAllResources call whose query MUST NOT include
// `labels.project:` — parent-scoped types are label-less by
// construction, so the server-side clause would unconditionally
// exclude every result. Location and other clauses must still
// propagate.
func TestDiscoverTypes_ParentBucketOnly_OneCallWithoutLabelsClause(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_fake_parent": &fakeParentNamePrefixDiscoverer{
				resourceType: "google_fake_parent",
				assetType:    "test.googleapis.com/ParentScoped",
				parentMarker: "/parents/",
			},
		},
	}
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_fake_parent"}, DiscoverArgs{
		Project: "io-foo",
		Regions: []string{"us-central1"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll calls=%d, want 1 (one bucket, one round-trip)", len(fake.calls))
	}
	if strings.Contains(fake.calls[0].query, "labels.project:") {
		t.Errorf("parent-bucket query=%q, want NO labels.project clause", fake.calls[0].query)
	}
	if !strings.Contains(fake.calls[0].query, "location:us-central1") {
		t.Errorf("parent-bucket query=%q, want location:us-central1 to still propagate", fake.calls[0].query)
	}
}

// TestDiscoverTypes_ParentNamePrefixFiltersOnParentSegment pins the
// third-bucket client-side filter (#381). The filter must check the
// parent path segment (between the discoverer's ParentMarker and the
// next "/"), NOT the trailing short name and NOT earlier path
// segments like /projects/<gcp-project-id>/.
//
// Four asset rows, ordered so that:
//
//   - the orchestrator's stable-sort-by-asset-Name puts a DROPPED
//     asset first (alphabetically before any survivor), so a mutation
//     that replaces the filter with `rs[:N]` (keep first N) fails.
//   - TWO assets survive (alpha-prefix and zeta-prefix rings, both
//     embedding "io-foo"), so a mutation that keeps only one
//     element fails.
//   - TWO assets are dropped, both adversarial in different ways
//     (project-in-gcp-id+short-name vs project-in-location).
//
// Uses the live kms_crypto_key discoverer end-to-end so the
// registered "/keyRings/" marker is exercised rather than a fake.
func TestDiscoverTypes_ParentNamePrefixFiltersOnParentSegment(t *testing.T) {
	t.Parallel()
	const tfType = "google_kms_crypto_key"
	// Asset names are sorted in the Cloud Asset response by the
	// orchestrator (gcpdiscover.go's stable-sort), and the sort key
	// is the full asset Name. The four entries here sort as:
	//
	//   1. //cloudkms.../keyRings/aaa-other-stack-ring/...      DROPPED
	//   2. //cloudkms.../keyRings/io-foo-alpha-ring/...         KEPT
	//   3. //cloudkms.../keyRings/io-foo-zeta-ring/...          KEPT
	//   4. //cloudkms.../keyRings/zzz-elsewhere/...             DROPPED
	//
	// The DROPPED entry at position 1 defeats the "keep first" mutation.
	// The KEPT entry at position 4 (zzz parent, but actually io-foo-zeta
	// — see below) — wait, no: the io-foo-* keys sort before zzz. Layout
	// after the orchestrator's stable-sort is exactly the order above.
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			// Adversarial: project-in-gcp-id AND in the trailing
			// short name, but the parent ring is "zzz-elsewhere".
			{Name: "//cloudkms.googleapis.com/projects/io-foo-real-proj/locations/us-central1/keyRings/zzz-elsewhere/cryptoKeys/io-foo-shaped-name", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
			// Adversarial: parent ring does NOT carry io-foo
			// (alphabetically first — sort makes this the first
			// element in the bucket).
			{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/aaa-other-stack-ring/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
			// KEPT: parent ring carries io-foo (alpha sort key
			// puts it second after stable sort).
			{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-alpha-ring/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
			// KEPT: a second io-foo-* ring; pins that the filter
			// keeps MULTIPLE matches, not just the first.
			{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-zeta-ring/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
		},
	}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			tfType: newKMSCryptoKeyDiscoverer(),
		},
	}
	got, err := g.DiscoverTypes(context.Background(), []string{tfType}, DiscoverArgs{
		Project: "io-foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Three cryptokey names collide on "default" / "default" /
	// "io-foo-shaped-name", and the trailing short name is "default"
	// for the survivors — making NameHint a poor discriminator. Pin
	// the parent ring via NativeIDs["key_ring"] (set by FromAsset
	// from the parsed /keyRings/<name>/ segment).
	if len(got) != 2 {
		t.Fatalf("kept=%d, want 2 (the two io-foo-* parent rings must survive); names=%v", len(got), namesOf(got))
	}
	gotRings := []string{
		got[0].Identity.NativeIDs["key_ring"],
		got[1].Identity.NativeIDs["key_ring"],
	}
	wantRings := []string{"io-foo-alpha-ring", "io-foo-zeta-ring"}
	if !reflect.DeepEqual(gotRings, wantRings) {
		t.Errorf("kept rings=%v, want %v (filter must keep parent-ring matches and drop non-matches)", gotRings, wantRings)
	}
}

// TestDiscoverTypes_ParentNamePrefixEmptyProjectPassesThrough pins
// the no-filter case for the parent bucket (#381), symmetric with the
// labels and name-prefix lanes: when args.Project is empty, the
// per-result parent-segment filter is skipped and every asset passes
// through.
//
// Antichain construction (same shape as
// TestDiscoverTypes_NamePrefixEmptyProjectPassesThrough): three
// parent rings (alpha/zeta/mid) with no substring overlap, all with
// the conventional "default" child name. A regression that ran the
// substring filter with an empty needle would still pass trivially
// (strings.Contains(x, "") == true), so the assertion checks every
// input survived AND in the orchestrator's stable-by-asset-Name
// order.
func TestDiscoverTypes_ParentNamePrefixEmptyProjectPassesThrough(t *testing.T) {
	t.Parallel()
	const tfType = "google_kms_crypto_key"
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/alpha/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
			{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/zeta/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
			{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/mid/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
		},
	}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			tfType: newKMSCryptoKeyDiscoverer(),
		},
	}
	got, err := g.DiscoverTypes(context.Background(), []string{tfType}, DiscoverArgs{
		Project: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Stable sort by full Cloud Asset Name puts alpha < mid < zeta.
	// NameHint is "default" for all three (cryptokey names collide on
	// "default" — the realistic shape that motivated #381) so pin via
	// the parent ring instead.
	if len(got) != 3 {
		t.Fatalf("kept=%d, want 3 (empty stack project must pass every result through); names=%v", len(got), namesOf(got))
	}
	wantRings := []string{"alpha", "mid", "zeta"}
	gotRings := make([]string, len(got))
	for i, r := range got {
		gotRings[i] = r.Identity.NativeIDs["key_ring"]
	}
	if !reflect.DeepEqual(gotRings, wantRings) {
		t.Errorf("kept rings=%v, want %v (every input must pass through unchanged when args.Project is empty)", gotRings, wantRings)
	}
}

// TestDiscoverTypes_MixedThreeBuckets_ThreeSearchCallsWithDifferentQueries
// pins the three-call dispatch when all three ScopeStyle buckets are
// non-empty (#381). Extension of the existing two-bucket
// mixed-buckets test:
//
//   - 3 SearchAllResources calls, one per ScopeStyle.
//   - Asset-type partition is strict — no asset type appears in
//     more than one call.
//   - Labels call carries `labels.project:io-foo`; the other two do not.
//   - The full result set (one per bucket) is returned.
func TestDiscoverTypes_MixedThreeBuckets_ThreeSearchCallsWithDifferentQueries(t *testing.T) {
	t.Parallel()
	fake := &bucketedFakeSearcher{
		resultsByAssetType: map[string][]gcpAssetResult{
			"pubsub.googleapis.com/Topic": {
				{Name: "//pubsub.googleapis.com/projects/real-proj/topics/io-foo-events", AssetType: "pubsub.googleapis.com/Topic", Project: "real-proj", Labels: map[string]string{"project": "io-foo"}},
			},
			"test.googleapis.com/NamePrefixed": {
				{Name: "//test.googleapis.com/projects/real-proj/widgets/io-foo-widget", AssetType: "test.googleapis.com/NamePrefixed", Project: "real-proj"},
			},
			"cloudkms.googleapis.com/CryptoKey": {
				{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Project: "real-proj", Location: "us-central1"},
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
			"google_kms_crypto_key": newKMSCryptoKeyDiscoverer(),
		},
	}
	got, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_fake_namepref", "google_kms_crypto_key"}, DiscoverArgs{
		Project: "io-foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 3 {
		t.Fatalf("SearchAll calls=%d, want 3 (one per ScopeStyle bucket); calls=%+v", len(fake.calls), fake.calls)
	}

	var labelsCall, nameCall, parentCall *searchAllCall
	for i := range fake.calls {
		c := &fake.calls[i]
		switch {
		case slices.Contains(c.assetTypes, "pubsub.googleapis.com/Topic"):
			labelsCall = c
		case slices.Contains(c.assetTypes, "test.googleapis.com/NamePrefixed"):
			nameCall = c
		case slices.Contains(c.assetTypes, "cloudkms.googleapis.com/CryptoKey"):
			parentCall = c
		}
	}
	if labelsCall == nil {
		t.Fatal("no SearchAll call carried the labels-bucket asset type")
	}
	if nameCall == nil {
		t.Fatal("no SearchAll call carried the name-prefix-bucket asset type")
	}
	if parentCall == nil {
		t.Fatal("no SearchAll call carried the parent-name-prefix-bucket asset type")
	}

	if !strings.Contains(labelsCall.query, "labels.project:io-foo") {
		t.Errorf("labels-bucket query=%q, want labels.project:io-foo", labelsCall.query)
	}
	if strings.Contains(nameCall.query, "labels.project:") {
		t.Errorf("name-prefix-bucket query=%q, want NO labels.project clause", nameCall.query)
	}
	if strings.Contains(parentCall.query, "labels.project:") {
		t.Errorf("parent-bucket query=%q, want NO labels.project clause", parentCall.query)
	}

	// Strict partition — no asset type leaks across buckets.
	for _, at := range []string{"test.googleapis.com/NamePrefixed", "cloudkms.googleapis.com/CryptoKey"} {
		if slices.Contains(labelsCall.assetTypes, at) {
			t.Errorf("labels-bucket call asset types=%v, must not include %q", labelsCall.assetTypes, at)
		}
	}
	for _, at := range []string{"pubsub.googleapis.com/Topic", "cloudkms.googleapis.com/CryptoKey"} {
		if slices.Contains(nameCall.assetTypes, at) {
			t.Errorf("name-prefix-bucket call asset types=%v, must not include %q", nameCall.assetTypes, at)
		}
	}
	for _, at := range []string{"pubsub.googleapis.com/Topic", "test.googleapis.com/NamePrefixed"} {
		if slices.Contains(parentCall.assetTypes, at) {
			t.Errorf("parent-bucket call asset types=%v, must not include %q", parentCall.assetTypes, at)
		}
	}

	// All three buckets contribute to the returned slice.
	if len(got) != 3 {
		t.Fatalf("ImportedResources=%d, want 3 (one per bucket); names=%v", len(got), namesOf(got))
	}
}

// TestDiscoverTypes_ThreeBucketsEmitOneServiceStartFinishPair extends
// the single-pair contract (#295) to the three-bucket path (#381):
// even when all three ScopeStyle buckets issue independent
// SearchAllResources calls, the orchestrator emits exactly one
// (service_start, service_finish) pair around the combined operation.
func TestDiscoverTypes_ThreeBucketsEmitOneServiceStartFinishPair(t *testing.T) {
	t.Parallel()
	fake := &bucketedFakeSearcher{
		resultsByAssetType: map[string][]gcpAssetResult{
			"pubsub.googleapis.com/Topic": {
				{Name: "//pubsub.googleapis.com/projects/real-proj/topics/io-foo-events", AssetType: "pubsub.googleapis.com/Topic"},
			},
			"test.googleapis.com/NamePrefixed": {
				{Name: "//test.googleapis.com/projects/real-proj/widgets/io-foo-widget", AssetType: "test.googleapis.com/NamePrefixed"},
			},
			"cloudkms.googleapis.com/CryptoKey": {
				{Name: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/default", AssetType: "cloudkms.googleapis.com/CryptoKey", Location: "us-central1"},
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
			"google_kms_crypto_key": newKMSCryptoKeyDiscoverer(),
		},
	}
	rec := &recordingEmitter{}
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_pubsub_topic", "google_fake_namepref", "google_kms_crypto_key"}, DiscoverArgs{
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
		case "service_finish":
			finishes++
		}
	}
	if starts != 1 {
		t.Errorf("service_start count=%d, want 1 (one pair across all three ScopeStyle buckets)", starts)
	}
	if finishes != 1 {
		t.Errorf("service_finish count=%d, want 1 (one pair across all three ScopeStyle buckets)", finishes)
	}
}

// TestMatchesParentNamePrefix pins the parent-segment matcher's
// contract (#381). Same adversarial shape as TestMatchesNamePrefix:
// the load-bearing rows are the ones that pin what the matcher MUST
// NOT match. A regression that fell back to substring on the full
// asset name (or on the trailing short name) would fail loudly.
func TestMatchesParentNamePrefix(t *testing.T) {
	t.Parallel()
	const ringMarker = "/keyRings/"
	cases := []struct {
		name      string
		assetName string
		marker    string
		project   string
		want      bool
	}{
		// Happy paths.
		{
			name:      "parent contains project as prefix",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-main/cryptoKeys/default",
			marker:    ringMarker, project: "io-foo", want: true,
		},
		{
			name:      "parent equals project exactly",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo/cryptoKeys/default",
			marker:    ringMarker, project: "io-foo", want: true,
		},
		{
			name:      "parent contains project as infix",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/main-io-foo-ring/cryptoKeys/default",
			marker:    ringMarker, project: "io-foo", want: true,
		},
		{
			name:      "parent does not contain project",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/other-stack/cryptoKeys/default",
			marker:    ringMarker, project: "io-foo", want: false,
		},

		// Adversarial: stack project appears in earlier path segments
		// (GCP project ID or location) while the parent ring is owned
		// by a different stack. Must NOT match.
		{
			name:      "project in leading GCP project ID segment, not in parent",
			assetName: "//cloudkms.googleapis.com/projects/io-foo-real-proj/locations/us-central1/keyRings/elsewhere/cryptoKeys/default",
			marker:    ringMarker, project: "io-foo", want: false,
		},
		{
			name:      "project in location segment, not in parent",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/io-foo-zone/keyRings/other-stack/cryptoKeys/default",
			marker:    ringMarker, project: "io-foo", want: false,
		},

		// Adversarial: project appears in the trailing child short name
		// but NOT in the parent. The parent-bucket filter must consult
		// the parent segment, not the short name — otherwise child
		// names conventionally embedding the stack would mask the
		// parent-name convention this scope style exists to enforce.
		{
			name:      "project in child short name only, not in parent",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/elsewhere/cryptoKeys/io-foo-shaped-name",
			marker:    ringMarker, project: "io-foo", want: false,
		},

		// Edge cases.
		{
			name:      "marker absent — defensive false (fails closed)",
			assetName: "//pubsub.googleapis.com/projects/real-proj/topics/io-foo-events",
			marker:    ringMarker, project: "io-foo", want: false,
		},
		{
			name:      "empty parent segment between markers",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings//cryptoKeys/default",
			marker:    ringMarker, project: "io-foo", want: false,
		},
		{
			name:      "empty asset name",
			assetName: "",
			marker:    ringMarker, project: "io-foo", want: false,
		},
		{
			name:      "marker is the last token (no trailing slash) — full remainder is the parent",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-tail",
			marker:    ringMarker, project: "io-foo", want: true,
		},
		// Programmer-error surface: empty marker is the caller's
		// responsibility (searchBuckets rejects empty markers at the
		// per-discoverer guard — pinned by
		// TestSearchBuckets_ParentEmptyMarker_FailsLoud). Not exercised
		// here because the helper's behavior with sep="" depends on
		// where the first "/" lands in the asset string, which makes
		// the result implementation-defined from the helper's point of
		// view. The contract-pinning test sits at the caller layer.
		//
		// Programmer-error surface: empty project. strings.Contains
		// with an empty needle returns true, so any non-empty parent
		// segment "matches". Documented behavior — caller
		// (searchBuckets's `if args.Project != ""` guard at
		// gcpdiscover.go:472) MUST skip this filter for empty
		// projects. Pin the behavior so a "harden the helper" change
		// silently flipping it surfaces here.
		{
			name:      "empty project — caller responsibility (vacuous true match)",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/anyring/cryptoKeys/default",
			marker:    ringMarker, project: "", want: true,
		},
		// Marker appearing twice. strings.Cut splits on the first
		// occurrence, so the parent is extracted from the FIRST
		// /keyRings/ — the second is ignored as just another segment
		// inside the child path. Pin the first-occurrence behavior
		// against a refactor that swapped to LastIndex.
		{
			name:      "marker appears twice — first occurrence wins",
			assetName: "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/x/keyRings/decoy-not-io-foo",
			marker:    ringMarker, project: "io-foo", want: true,
		},

		// Clusters marker — pin that the helper is marker-generic, not
		// keyrings-specific.
		{
			name:      "different marker (clusters) — parent contains project",
			assetName: "//container.googleapis.com/projects/real-proj/locations/us-central1/clusters/io-foo-gke/nodePools/default-pool",
			marker:    "/clusters/", project: "io-foo", want: true,
		},
		{
			name:      "different marker (clusters) — parent does not contain project",
			assetName: "//container.googleapis.com/projects/real-proj/locations/us-central1/clusters/other-gke/nodePools/default-pool",
			marker:    "/clusters/", project: "io-foo", want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matchesParentNamePrefix(tc.assetName, tc.marker, tc.project); got != tc.want {
				t.Errorf("matchesParentNamePrefix(%q, %q, %q) = %v, want %v", tc.assetName, tc.marker, tc.project, got, tc.want)
			}
		})
	}
}

// TestSearchBuckets_ParentMissingSideInterface_FailsLoud pins one of
// the two programmer-error returns in searchBuckets (#381). A
// Discoverer that reports ScopeStyleParentNamePrefix but does NOT
// implement parentScopedDiscoverer (no ParentMarker method) must
// cause DiscoverTypes to fail with a descriptive error rather than
// silently route around it. Without this test, a refactor that
// demoted the error to a continue would ship green.
//
// TestParentScopedDiscoverer_ContractMatchesScopeStyle covers the
// live-registry side of the contract; this covers the runtime
// fault-injection side.
func TestSearchBuckets_ParentMissingSideInterface_FailsLoud(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_broken_parent": &brokenParentScopeDiscoverer{
				resourceType: "google_broken_parent",
				assetType:    "test.googleapis.com/BrokenParent",
			},
		},
	}
	_, err := g.DiscoverTypes(context.Background(), []string{"google_broken_parent"}, DiscoverArgs{
		Project: "io-foo",
	})
	if err == nil {
		t.Fatal("DiscoverTypes returned nil error; expected programmer-error return for missing parentScopedDiscoverer")
	}
	if !strings.Contains(err.Error(), "does not implement parentScopedDiscoverer") {
		t.Errorf("err=%v, want error mentioning the missing-side-interface contract", err)
	}
	if !strings.Contains(err.Error(), "google_broken_parent") {
		t.Errorf("err=%v, want error to name the offending type", err)
	}
}

// TestSearchBuckets_ParentEmptyMarker_FailsLoud pins the second
// programmer-error return in searchBuckets (#381): a parent-scoped
// discoverer that DOES implement the side-interface but returns ""
// from ParentMarker. Returning empty would short-circuit
// matchesParentNamePrefix's match (defensive false), silently
// dropping every asset of that type — far worse than a loud error
// at search time.
func TestSearchBuckets_ParentEmptyMarker_FailsLoud(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_empty_marker": &fakeParentNamePrefixDiscoverer{
				resourceType: "google_empty_marker",
				assetType:    "test.googleapis.com/EmptyMarker",
				parentMarker: "", // the programmer-error shape
			},
		},
	}
	_, err := g.DiscoverTypes(context.Background(), []string{"google_empty_marker"}, DiscoverArgs{
		Project: "io-foo",
	})
	if err == nil {
		t.Fatal("DiscoverTypes returned nil error; expected programmer-error return for empty ParentMarker")
	}
	if !strings.Contains(err.Error(), "ParentMarker() returned empty string") {
		t.Errorf("err=%v, want error mentioning the empty-marker contract", err)
	}
	if !strings.Contains(err.Error(), "google_empty_marker") {
		t.Errorf("err=%v, want error to name the offending type", err)
	}
}

// TestSearchBuckets_ParentEmptyMarker_SkippedWhenEmptyProject pins
// that the programmer-error checks fire from inside the
// `if args.Project != ""` guard — when args.Project is empty the
// filter is skipped and the broken marker never trips. Symmetric
// with the empty-project pass-through tests for the other two
// buckets. Pinning this surface so a regression that moved the
// marker-validation outside the guard (e.g. to NewGCPDiscoverer)
// surfaces here; conversely, if a future refactor *intentionally*
// moves the validation to wiring time, this test must be updated
// to expect the error at construction.
func TestSearchBuckets_ParentEmptyMarker_SkippedWhenEmptyProject(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := &GCPDiscoverer{
		searcher:  fake,
		projectID: "real-proj",
		byType: map[string]Discoverer{
			"google_empty_marker": &fakeParentNamePrefixDiscoverer{
				resourceType: "google_empty_marker",
				assetType:    "test.googleapis.com/EmptyMarker",
				parentMarker: "",
			},
		},
	}
	if _, err := g.DiscoverTypes(context.Background(), []string{"google_empty_marker"}, DiscoverArgs{
		Project: "",
	}); err != nil {
		t.Errorf("DiscoverTypes(empty project)=%v, want nil — marker validation MUST be inside the args.Project guard", err)
	}
}
