package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestKMSCryptoKeyFromAsset(t *testing.T) {
	t.Parallel()
	d := newKMSCryptoKeyDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//cloudkms.googleapis.com/projects/real-proj/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/io-foo-key",
			AssetType: "cloudkms.googleapis.com/CryptoKey",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_kms_crypto_key" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-key" {
		t.Errorf("NameHint=%q, want io-foo-key", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/io-foo-key"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.NativeIDs["key_ring"] != "io-foo-ring" {
		t.Errorf("NativeIDs[key_ring]=%q, want io-foo-ring", got.Identity.NativeIDs["key_ring"])
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
}

func TestKMSCryptoKeyDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newKMSCryptoKeyDiscoverer()
	cases := []struct {
		name, in, wantName, wantRing, wantLoc string
		wantErr                               error
	}{
		{name: "asset name", in: "//cloudkms.googleapis.com/projects/p/locations/global/keyRings/r1/cryptoKeys/k1", wantName: "k1", wantRing: "r1", wantLoc: "global"},
		{name: "import id", in: "projects/p/locations/us-central1/keyRings/r1/cryptoKeys/k1", wantName: "k1", wantRing: "r1", wantLoc: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected", in: "k1", wantErr: ErrNotSupported},
		{name: "missing keyring parent", in: "projects/p/locations/us-central1/cryptoKeys/k1", wantErr: ErrNotSupported},
		{name: "missing cryptokey segment", in: "projects/p/locations/us-central1/keyRings/r1", wantErr: ErrNotSupported},
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
			if got.Identity.NativeIDs["key_ring"] != tc.wantRing {
				t.Errorf("NativeIDs[key_ring]=%q, want %q", got.Identity.NativeIDs["key_ring"], tc.wantRing)
			}
			if got.Identity.Location != tc.wantLoc {
				t.Errorf("Location=%q, want %q", got.Identity.Location, tc.wantLoc)
			}
		})
	}
}
