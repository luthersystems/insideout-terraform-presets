package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestKMSKeyRingFromAsset(t *testing.T) {
	t.Parallel()
	d := newKMSKeyRingDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-ring",
			AssetType: "cloudkms.googleapis.com/KeyRing",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_kms_key_ring" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-ring" {
		t.Errorf("NameHint=%q, want io-foo-ring", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/keyRings/io-foo-ring"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
}

func TestKMSKeyRingRecoversLocationFromAssetNameWhenLocationFieldEmpty(t *testing.T) {
	t.Parallel()
	// Cloud Asset has been observed to omit the Location field on
	// keyrings even though the asset path carries /locations/<loc>/.
	// Pinning the recovery path so Identity.Location and the import
	// ID stay aligned.
	d := newKMSKeyRingDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//cloudkms.googleapis.com/projects/real-proj/locations/global/keyRings/io-foo-ring",
			AssetType: "cloudkms.googleapis.com/KeyRing",
			Project:   "real-proj",
			// Location intentionally empty.
		},
		"real-proj")
	if got.Identity.Location != "global" {
		t.Errorf("Location=%q, want global (recovered from asset name)", got.Identity.Location)
	}
	if got.Identity.ImportID != "projects/real-proj/locations/global/keyRings/io-foo-ring" {
		t.Errorf("ImportID=%q lost the location segment", got.Identity.ImportID)
	}
}

func TestKMSKeyRingDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newKMSKeyRingDiscoverer()
	cases := []struct {
		name, in, wantName, wantLoc string
		wantErr                     error
	}{
		{name: "asset name", in: "//cloudkms.googleapis.com/projects/p/locations/global/keyRings/io-foo-ring", wantName: "io-foo-ring", wantLoc: "global"},
		{name: "import id", in: "projects/p/locations/us-central1/keyRings/io-foo-ring", wantName: "io-foo-ring", wantLoc: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (location required)", in: "io-foo-ring", wantErr: ErrNotSupported},
		{name: "missing locations segment", in: "projects/p/keyRings/io-foo-ring", wantErr: ErrNotSupported},
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
