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

func TestComputeAddressFromAsset_Global(t *testing.T) {
	t.Parallel()
	d := newComputeAddressDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/addresses/io-foo-lb-ip",
			AssetType: "compute.googleapis.com/Address",
			Project:   "real-proj",
			// Global addresses have no Location.
		},
		"real-proj")
	wantImport := "projects/real-proj/global/addresses/io-foo-lb-ip"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (global address)", got.Identity.Location)
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
		{name: "global asset name", in: "//compute.googleapis.com/projects/p/global/addresses/lb-ip", wantName: "lb-ip", wantRegion: ""},
		{name: "global import id", in: "projects/p/global/addresses/lb-ip", wantName: "lb-ip", wantRegion: ""},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (region or global required)", in: "ip1", wantErr: ErrNotSupported},
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
