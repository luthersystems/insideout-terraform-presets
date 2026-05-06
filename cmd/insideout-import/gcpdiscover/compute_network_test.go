package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeNetworkFromAsset(t *testing.T) {
	t.Parallel()
	d := newComputeNetworkDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/networks/io-foo-vpc",
			AssetType: "compute.googleapis.com/Network",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_network" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-vpc" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.ImportID != "projects/real-proj/global/networks/io-foo-vpc" {
		t.Errorf("ImportID=%q, want projects/real-proj/global/networks/io-foo-vpc", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["self_link"] == "" {
		t.Error("NativeIDs[self_link] empty")
	}
}

func TestComputeNetworkDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeNetworkDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/global/networks/vpc-main", wantName: "vpc-main"},
		{name: "self link v1", in: "https://www.googleapis.com/compute/v1/projects/p/global/networks/vpc-main", wantName: "vpc-main"},
		{name: "import id", in: "projects/p/global/networks/vpc-main", wantName: "vpc-main"},
		{name: "bare name", in: "vpc-main", wantName: "vpc-main"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := d.DiscoverByID(context.Background(), nil, tc.in, "p")
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
		})
	}
}
