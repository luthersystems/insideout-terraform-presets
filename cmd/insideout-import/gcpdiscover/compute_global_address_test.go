package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

// TestComputeGlobalAddressFromAsset_Global pins the happy path for
// the global discoverer (#384): a //compute.googleapis.com/.../global/
// addresses/... row produces a real ImportedResource with the
// projects/<p>/global/addresses/<n> import-ID and an empty Location
// (since "global" is a path segment, not a GCP location).
func TestComputeGlobalAddressFromAsset_Global(t *testing.T) {
	t.Parallel()
	d := newComputeGlobalAddressDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/addresses/io-foo-lb-ip",
			AssetType: "compute.googleapis.com/Address",
			Project:   "real-proj",
			Location:  "global",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_global_address" {
		t.Errorf("Type=%q, want google_compute_global_address", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-lb-ip" {
		t.Errorf("NameHint=%q, want io-foo-lb-ip", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/global/addresses/io-foo-lb-ip"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (globals carry no location)", got.Identity.Location)
	}
}

// TestComputeGlobalAddressFromAsset_Regional_IsSkipped pins the
// inverse skip-sentinel contract (#384) — the global discoverer must
// drop regional rows so its emit set is disjoint from the regional
// discoverer's. Mirrors TestComputeAddressFromAsset_Global_IsSkipped
// on the regional side.
func TestComputeGlobalAddressFromAsset_Regional_IsSkipped(t *testing.T) {
	t.Parallel()
	d := newComputeGlobalAddressDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/addresses/io-foo-ip",
			AssetType: "compute.googleapis.com/Address",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "" {
		t.Errorf("Identity.Type=%q, want empty (regional address must be skipped — orchestrator filters out zero-Type rows; regional rows belong to google_compute_address)", got.Identity.Type)
	}
	if got.Identity.ImportID != "" {
		t.Errorf("Identity.ImportID=%q, want empty", got.Identity.ImportID)
	}
}

func TestComputeGlobalAddressDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeGlobalAddressDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "global asset name", in: "//compute.googleapis.com/projects/p/global/addresses/lb-ip", wantName: "lb-ip"},
		{name: "global import id", in: "projects/p/global/addresses/lb-ip", wantName: "lb-ip"},
		// Regional rows are symmetrically rejected — they belong to
		// google_compute_address, the regional sibling. Without this
		// rejection the dep-chase code path would emit a malformed
		// `projects/<p>/global/addresses/...` import-id for a regional
		// resource (the inverse of the bug the regional discoverer
		// guards against).
		{name: "regional asset name rejected (different TF type)", in: "//compute.googleapis.com/projects/p/regions/us-east1/addresses/ip1", wantErr: ErrNotSupported},
		{name: "regional import id rejected (different TF type)", in: "projects/p/regions/us-east1/addresses/ip1", wantErr: ErrNotSupported},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (no /global/addresses/ marker)", in: "lb-ip", wantErr: ErrNotSupported},
		// Adversarial: the substring "/global/addresses/" but with
		// empty name after the trailing slash — production CAI never
		// emits this shape, but pinning the empty-name guard surfaces
		// a regression that dropped it.
		{name: "trailing-slash empty name rejected", in: "projects/p/global/addresses/", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := d.DiscoverByID(context.Background(), nil, tc.in, "real-proj")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Identity.NameHint != tc.wantName {
				t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, tc.wantName)
			}
			if got.Identity.Type != "google_compute_global_address" {
				t.Errorf("Type=%q, want google_compute_global_address", got.Identity.Type)
			}
			if got.Identity.Location != "" {
				t.Errorf("Location=%q, want empty (globals carry no location)", got.Identity.Location)
			}
		})
	}
}

// TestDiscoverTypes_AddressRegionalAndGlobal_CoexistOnSharedSlug is
// the acceptance test from #384: when the live registry holds BOTH
// google_compute_address and google_compute_global_address (sharing
// the compute.googleapis.com/Address CAI slug), an asset bucket
// containing one regional + one global with identical short names
// must produce one ImportedResource of each TF type.
//
// Three properties under test:
//
//  1. assetTypesOf's dedup at gcpdiscover.go (#384) — the asset-type
//     list passed to SearchAll must NOT carry duplicates even though
//     two discoverers register the same slug.
//  2. Each discoverer's FromAsset filter is the inverse of the other,
//     so the regional row goes to google_compute_address and the
//     global row goes to google_compute_global_address.
//  3. No row is dropped or doubled.
//
// Uses identical short names on both rows to defeat any short-name-
// based partition logic that might be inadvertently introduced — the
// only correct discriminator is the /regions/<r>/ vs /global/ path
// marker.
func TestDiscoverTypes_AddressRegionalAndGlobal_CoexistOnSharedSlug(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{
				Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/addresses/io-foo-shared",
				AssetType: "compute.googleapis.com/Address",
				Project:   "real-proj",
				Location:  "us-central1",
				Labels:    map[string]string{"project": "io-foo"},
			},
			{
				Name:      "//compute.googleapis.com/projects/real-proj/global/addresses/io-foo-shared",
				AssetType: "compute.googleapis.com/Address",
				Project:   "real-proj",
				Location:  "global",
				Labels:    map[string]string{"project": "io-foo"},
			},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})
	got, err := g.DiscoverTypes(context.Background(),
		[]string{"google_compute_address", "google_compute_global_address"},
		DiscoverArgs{Project: "io-foo"})
	if err != nil {
		t.Fatal(err)
	}
	// One SearchAll call (both discoverers labels-bucket, same slug).
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll calls=%d, want 1 — assetTypesOf must dedup shared CAI slugs", len(fake.calls))
	}
	// Dedup pins: the assetTypes slice on the call has exactly one
	// entry, not two.
	if len(fake.calls[0].assetTypes) != 1 {
		t.Errorf("call.assetTypes=%v, want exactly 1 entry (deduped)", fake.calls[0].assetTypes)
	}
	if len(got) != 2 {
		t.Fatalf("got %d ImportedResources, want 2 (one regional + one global); got: %+v", len(got), got)
	}
	// Pin partition: one of each TF type. The orchestrator's emit
	// loop iterates discoverers in their registration order; the
	// global comes after the regional alphabetically, but pinning
	// the set membership is more mutation-resistant than the order.
	// Pin the type-to-import-id binding directly per resource, not via
	// sorted-set membership. A mutation that inverted the per-discoverer
	// filter would still produce {regional, global} type membership
	// (both discoverers still iterate the bucket and emit one each) —
	// but the regional discoverer processing a global asset would
	// produce a malformed ImportID like
	// `projects/real-proj/regions/global/addresses/...` (asset's
	// Location="global" flows into the region slot). Pinning the
	// type→import-id pair catches this directly rather than relying on
	// the malformed import-id leaking into a sorted-slice assertion.
	byType := make(map[string]string, len(got))
	for _, r := range got {
		if _, dup := byType[r.Identity.Type]; dup {
			t.Errorf("duplicate emit for type %q (got: %+v)", r.Identity.Type, got)
		}
		byType[r.Identity.Type] = r.Identity.ImportID
	}
	if want, got := "projects/real-proj/regions/us-central1/addresses/io-foo-shared", byType["google_compute_address"]; got != want {
		t.Errorf("google_compute_address.ImportID = %q, want %q (regional discoverer must process the /regions/<r>/ row)", got, want)
	}
	if want, got := "projects/real-proj/global/addresses/io-foo-shared", byType["google_compute_global_address"]; got != want {
		t.Errorf("google_compute_global_address.ImportID = %q, want %q (global discoverer must process the /global/ row)", got, want)
	}
}
