package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestContainerNodePoolFromAsset(t *testing.T) {
	t.Parallel()
	d := newContainerNodePoolDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//container.googleapis.com/projects/real-proj/locations/us-central1/clusters/io-foo-gke/nodePools/io-foo-pool",
			AssetType: "container.googleapis.com/NodePool",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_container_node_pool" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-pool" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["cluster"] != "io-foo-gke" {
		t.Errorf("NativeIDs[cluster]=%q, want io-foo-gke", got.Identity.NativeIDs["cluster"])
	}
	wantImport := "projects/real-proj/locations/us-central1/clusters/io-foo-gke/nodePools/io-foo-pool"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
}

func TestContainerNodePoolDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newContainerNodePoolDiscoverer()
	cases := []struct {
		name, in, wantName, wantCluster, wantLoc string
		wantErr                                  error
	}{
		{name: "asset name", in: "//container.googleapis.com/projects/p/locations/us-central1/clusters/c1/nodePools/np1", wantName: "np1", wantCluster: "c1", wantLoc: "us-central1"},
		{name: "import id", in: "projects/p/locations/us-central1/clusters/c1/nodePools/np1", wantName: "np1", wantCluster: "c1", wantLoc: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected", in: "np1", wantErr: ErrNotSupported},
		{name: "missing cluster parent", in: "projects/p/locations/us-central1/nodePools/np1", wantErr: ErrNotSupported},
		{name: "missing nodePools segment", in: "projects/p/locations/us-central1/clusters/c1", wantErr: ErrNotSupported},
		// Parent-collision adversarial row: cluster name == node pool name.
		// Pins that markers (not segment index) disambiguate parent
		// from child.
		{name: "cluster name equals node pool name", in: "projects/p/locations/us-central1/clusters/shared/nodePools/shared", wantName: "shared", wantCluster: "shared", wantLoc: "us-central1"},
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
			if got.Identity.NativeIDs["cluster"] != tc.wantCluster {
				t.Errorf("NativeIDs[cluster]=%q, want %q", got.Identity.NativeIDs["cluster"], tc.wantCluster)
			}
			if got.Identity.Location != tc.wantLoc {
				t.Errorf("Location=%q, want %q", got.Identity.Location, tc.wantLoc)
			}
		})
	}
}
