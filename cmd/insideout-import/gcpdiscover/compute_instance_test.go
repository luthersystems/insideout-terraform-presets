package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeInstanceFromAsset(t *testing.T) {
	t.Parallel()
	d := newComputeInstanceDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/io-foo-vm",
			AssetType: "compute.googleapis.com/Instance",
			Project:   "real-proj",
			Location:  "us-central1-a",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_compute_instance" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-vm" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/zones/us-central1-a/instances/io-foo-vm"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "us-central1-a" {
		t.Errorf("Location=%q, want us-central1-a", got.Identity.Location)
	}
	if got.Identity.Tags["project"] != "io-foo" {
		t.Errorf("Tags[project]=%q, want io-foo (label passes through)", got.Identity.Tags["project"])
	}
}

func TestComputeInstanceDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeInstanceDiscoverer()
	cases := []struct {
		name, in, wantName, wantZone string
		wantErr                      error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/zones/us-east1-b/instances/vm1", wantName: "vm1", wantZone: "us-east1-b"},
		{name: "import id", in: "projects/p/zones/us-central1-a/instances/vm1", wantName: "vm1", wantZone: "us-central1-a"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (zone required)", in: "vm1", wantErr: ErrNotSupported},
		{name: "missing zones segment", in: "projects/p/instances/vm1", wantErr: ErrNotSupported},
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
			if got.Identity.Location != tc.wantZone {
				t.Errorf("Location=%q, want %q", got.Identity.Location, tc.wantZone)
			}
		})
	}
}
