package reversedisco

import (
	"reflect"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// #777 GCP closure-discovery parity. These tests pin the GCP analog of the
// AWS #739/#770 selection-closure scoping: gcpAggAdapter.DiscoverClosure
// builds a gcpdiscover.DiscoverArgs.ParentScope from the closure request's
// selected parents so CAI enumeration is scoped per-selected-parent instead
// of a project-wide DiscoverTypes sweep.
//
// The end-to-end "the scoped query narrows by the selected parent's name"
// assertion lives at the gcpdiscover level
// (TestDiscoverTypes_ParentScopeNarrowsToSelectedParents) — the
// gcpAssetSearcher seam returns an unexported type, so a fake searcher must
// live in that package. Here we pin the adapter's mapping
// (req.ParentResources -> ParentScope), the load-bearing wiring this
// package owns.

// TestGCPParentScope_KeysByParentAssetType proves the #777 scoping builds
// the per-Cloud-Asset-type selected-parent scope from the closure request:
// each selected parent whose Terraform type maps to a registered asset type
// contributes its name (NameHint, falling back to ImportID) and location
// under the parent's asset type; parents with no registered discoverer are
// skipped. Mirror of TestAWSParentScope_KeysByParentCFNType.
func TestGCPParentScope_KeysByParentAssetType(t *testing.T) {
	t.Parallel()
	// A real GCPDiscoverer (nil searcher is fine — gcpParentScope only
	// reads the type registry via AssetTypeForTF, no network).
	a := gcpAggAdapter{d: gcpdiscover.NewGCPDiscoverer(nil, "real-proj", gcpdiscover.GCPDiscovererOpts{})}

	parents := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", NameHint: "io-uploads", Location: "us"}},
		// Same type, different location: kept separately.
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", NameHint: "io-logs", Location: "eu"}},
		// ImportID fallback when NameHint is empty.
		{Identity: imported.ResourceIdentity{Type: "google_pubsub_topic", ImportID: "io-events"}},
		// A type with no registered discoverer is skipped (no panic, no entry).
		{Identity: imported.ResourceIdentity{Type: "google_not_a_real_type", NameHint: "x"}},
	}
	scope := a.gcpParentScope(parents)

	// Location is carried through into the scope but is informational on
	// GCP today — buildScopedSearchQuery narrows by args.Regions, NOT by the
	// per-parent ScopedParent.Location (the CAI path expresses location in
	// the single bulk query, unlike the AWS per-region loop). We pin the
	// carry-through here so a future location-narrowed query has the value
	// available; the no-op-on-query behavior is pinned at the gcpdiscover
	// level (TestBuildScopedSearchQuery / TestDiscoverTypes_ParentScope*).
	wantBuckets := []gcpdiscover.ScopedParent{
		{Name: "io-logs", Location: "eu"},
		{Name: "io-uploads", Location: "us"},
	}
	if got := scope["storage.googleapis.com/Bucket"]; !reflect.DeepEqual(got, wantBuckets) {
		t.Errorf("storage bucket scope = %v, want %v", got, wantBuckets)
	}
	if got := scope["pubsub.googleapis.com/Topic"]; !reflect.DeepEqual(got, []gcpdiscover.ScopedParent{{Name: "io-events"}}) {
		t.Errorf("pubsub topic scope = %v, want [{io-events }]", got)
	}
	for assetType := range scope {
		switch assetType {
		case "storage.googleapis.com/Bucket", "pubsub.googleapis.com/Topic":
		default:
			t.Errorf("unexpected scope key %q (unknown-type parent should be skipped)", assetType)
		}
	}
	// Guard the gcpdiscover seam this relies on.
	if at, ok := a.d.AssetTypeForTF("google_storage_bucket"); !ok || at != "storage.googleapis.com/Bucket" {
		t.Errorf("AssetTypeForTF(google_storage_bucket) = (%q, %v), want (storage.googleapis.com/Bucket, true)", at, ok)
	}
	if _, ok := a.d.AssetTypeForTF("google_not_a_real_type"); ok {
		t.Error("AssetTypeForTF should return false for an unknown type")
	}
}

// TestGCPParentScope_EmptyWhenNoUsableParents proves a closure request with
// no registered-type parents yields a nil scope, so DiscoverClosure falls
// back to the project-wide sweep (no behavior change for selections that
// can't be scoped).
func TestGCPParentScope_EmptyWhenNoUsableParents(t *testing.T) {
	t.Parallel()
	a := gcpAggAdapter{d: gcpdiscover.NewGCPDiscoverer(nil, "real-proj", gcpdiscover.GCPDiscovererOpts{})}
	if got := a.gcpParentScope(nil); got != nil {
		t.Errorf("empty parents → scope %v, want nil", got)
	}
	if got := a.gcpParentScope([]imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "google_not_a_real_type", NameHint: "x"}},
	}); got != nil {
		t.Errorf("unregistered-type parent → scope %v, want nil", got)
	}
}
