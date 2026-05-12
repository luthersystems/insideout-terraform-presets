package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeAddressFromAsset_Regional(t *testing.T) {
	t.Parallel()
	d := newComputeAddressDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/addresses/io-foo-ip",
			AssetType: "compute.googleapis.com/Address",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.NameHint != "io-foo-ip" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/regions/us-central1/addresses/io-foo-ip"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
}

// TestComputeAddressFromAsset_Global_IsSkipped pins the post-#375
// behavior: global addresses belong to google_compute_global_address
// (a separate TF type, not in Bundle 8). The regional-address
// discoverer must skip them — returning a zero ImportedResource so
// the orchestrator drops the row instead of emitting an invalid
// `projects/<p>/regions/global/...` import-id. Without this skip,
// the live smoke against diagramtest2025-09-14 produced a malformed
// import-id for the stack's global LB IP.
func TestComputeAddressFromAsset_Global_IsSkipped(t *testing.T) {
	t.Parallel()
	d := newComputeAddressDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/addresses/io-foo-lb-ip",
			AssetType: "compute.googleapis.com/Address",
			Project:   "real-proj",
			Location:  "global",
		},
		"real-proj")
	if got.Identity.Type != "" {
		t.Errorf("Identity.Type=%q, want empty (global address must be skipped — orchestrator filters out zero-Type rows)", got.Identity.Type)
	}
	if got.Identity.ImportID != "" {
		t.Errorf("Identity.ImportID=%q, want empty", got.Identity.ImportID)
	}
}

func TestComputeAddressDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeAddressDiscoverer()
	cases := []struct {
		name, in, wantName, wantRegion string
		wantErr                        error
	}{
		{name: "regional asset name", in: "//compute.googleapis.com/projects/p/regions/us-east1/addresses/ip1", wantName: "ip1", wantRegion: "us-east1"},
		{name: "regional import id", in: "projects/p/regions/us-central1/addresses/ip1", wantName: "ip1", wantRegion: "us-central1"},
		// Global rows are symmetrically rejected by DiscoverByID
		// (mirroring the FromAsset skip): they belong to
		// google_compute_global_address, not google_compute_address.
		// Without this rejection the dep-chase code path would
		// resurrect the live-smoke malformed-import-id bug for
		// globals.
		{name: "global asset name rejected (different TF type)", in: "//compute.googleapis.com/projects/p/global/addresses/lb-ip", wantErr: ErrNotSupported},
		{name: "global import id rejected (different TF type)", in: "projects/p/global/addresses/lb-ip", wantErr: ErrNotSupported},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (region required)", in: "ip1", wantErr: ErrNotSupported},
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
			if got.Identity.Location != tc.wantRegion {
				t.Errorf("Location=%q, want %q", got.Identity.Location, tc.wantRegion)
			}
		})
	}
}
