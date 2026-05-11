package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestContainerClusterFromAsset(t *testing.T) {
	t.Parallel()
	d := newContainerClusterDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//container.googleapis.com/projects/real-proj/locations/us-central1/clusters/io-foo-gke",
			AssetType: "container.googleapis.com/Cluster",
			Project:   "real-proj",
			Location:  "us-central1",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_container_cluster" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-gke" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/clusters/io-foo-gke"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
}

func TestContainerClusterFromAsset_ZonalLocation(t *testing.T) {
	t.Parallel()
	// GKE clusters can be either regional or zonal; the discoverer
	// passes Location verbatim — the provider import accepts either.
	d := newContainerClusterDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//container.googleapis.com/projects/real-proj/locations/us-central1-a/clusters/zonal-cluster",
			AssetType: "container.googleapis.com/Cluster",
			Project:   "real-proj",
			Location:  "us-central1-a",
		},
		"real-proj")
	if got.Identity.Location != "us-central1-a" {
		t.Errorf("Location=%q, want us-central1-a (zonal — must pass through verbatim)", got.Identity.Location)
	}
}

func TestContainerClusterDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newContainerClusterDiscoverer()
	cases := []struct {
		name, in, wantName, wantLoc string
		wantErr                     error
	}{
		{name: "asset name regional", in: "//container.googleapis.com/projects/p/locations/us-central1/clusters/c1", wantName: "c1", wantLoc: "us-central1"},
		{name: "asset name zonal", in: "//container.googleapis.com/projects/p/locations/us-central1-a/clusters/c1", wantName: "c1", wantLoc: "us-central1-a"},
		{name: "import id", in: "projects/p/locations/us-central1/clusters/c1", wantName: "c1", wantLoc: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected", in: "c1", wantErr: ErrNotSupported},
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
			if got.Identity.Location != tc.wantLoc {
				t.Errorf("Location=%q, want %q", got.Identity.Location, tc.wantLoc)
			}
		})
	}
}
