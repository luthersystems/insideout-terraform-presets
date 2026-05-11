package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeRouterFromAsset(t *testing.T) {
	t.Parallel()
	d := newComputeRouterDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/routers/io-foo-router",
			AssetType: "compute.googleapis.com/Router",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_router" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-router" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/regions/us-central1/routers/io-foo-router"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
}

func TestComputeRouterRecoversRegionFromAssetNameWhenLocationFieldEmpty(t *testing.T) {
	t.Parallel()
	d := newComputeRouterDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-west1/routers/io-foo-router",
			AssetType: "compute.googleapis.com/Router",
		},
		"real-proj")
	if got.Identity.Location != "us-west1" {
		t.Errorf("Location=%q, want us-west1 (recovered from asset name)", got.Identity.Location)
	}
}

func TestComputeRouterDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeRouterDiscoverer()
	cases := []struct {
		name, in, wantName, wantRegion string
		wantErr                        error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/regions/us-east1/routers/r1", wantName: "r1", wantRegion: "us-east1"},
		{name: "import id", in: "projects/p/regions/us-central1/routers/r1", wantName: "r1", wantRegion: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (region required)", in: "r1", wantErr: ErrNotSupported},
		{name: "missing regions segment", in: "projects/p/routers/r1", wantErr: ErrNotSupported},
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
