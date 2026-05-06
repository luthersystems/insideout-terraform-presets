package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestStorageBucketFromAsset(t *testing.T) {
	t.Parallel()
	d := newStorageBucketDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//storage.googleapis.com/io-foo-bucket",
			AssetType: "storage.googleapis.com/Bucket",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_storage_bucket" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-bucket" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	// Bucket import IDs are the bare name — different from every other
	// GCP type. Pin it: a future refactor that emitted projects/<p>/<n>
	// would silently break terraform plan -generate-config-out.
	if got.Identity.ImportID != "io-foo-bucket" {
		t.Errorf("ImportID=%q, want bare bucket name", got.Identity.ImportID)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
	if got.Identity.NativeIDs["self_link"] == "" {
		t.Error("NativeIDs[self_link] empty")
	}
}

func TestStorageBucketDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newStorageBucketDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//storage.googleapis.com/io-foo-bucket", wantName: "io-foo-bucket"},
		{name: "self link v1", in: "https://www.googleapis.com/storage/v1/b/io-foo-bucket", wantName: "io-foo-bucket"},
		{name: "bare name", in: "io-foo-bucket", wantName: "io-foo-bucket"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized arn", in: "arn:aws:s3:::io-foo-bucket", wantErr: ErrNotSupported},
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
			if got.Identity.ImportID != tc.wantName {
				t.Errorf("ImportID=%q, want bare name %q", got.Identity.ImportID, tc.wantName)
			}
		})
	}
}
