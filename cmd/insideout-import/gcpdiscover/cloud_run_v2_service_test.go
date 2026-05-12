package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestCloudRunV2ServiceFromAsset(t *testing.T) {
	t.Parallel()
	d := newCloudRunV2ServiceDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//run.googleapis.com/projects/real-proj/locations/us-central1/services/io-foo-api",
			AssetType: "run.googleapis.com/Service",
			Project:   "real-proj",
			Location:  "us-central1",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_cloud_run_v2_service" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-api" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/services/io-foo-api"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
}

func TestCloudRunV2ServiceDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newCloudRunV2ServiceDiscoverer()
	cases := []struct {
		name, in, wantName, wantLoc string
		wantErr                     error
	}{
		{name: "asset name", in: "//run.googleapis.com/projects/p/locations/us-central1/services/s1", wantName: "s1", wantLoc: "us-central1"},
		{name: "import id", in: "projects/p/locations/us-central1/services/s1", wantName: "s1", wantLoc: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected", in: "s1", wantErr: ErrNotSupported},
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
