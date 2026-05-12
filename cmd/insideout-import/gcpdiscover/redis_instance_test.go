package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestRedisInstanceFromAsset(t *testing.T) {
	t.Parallel()
	d := newRedisInstanceDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//redis.googleapis.com/projects/real-proj/locations/us-central1/instances/io-foo-cache",
			AssetType: "redis.googleapis.com/Instance",
			Project:   "real-proj",
			Location:  "us-central1",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_redis_instance" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-cache" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/instances/io-foo-cache"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
	// ScopeStyleLabels relies on a.Labels flowing through to Tags so
	// the server-side labels.project filter attribution is preserved.
	// A mutation dropping a.Labels would slip through without this.
	if got.Identity.Tags["project"] != "io-foo" {
		t.Errorf("Tags[project]=%q, want %q", got.Identity.Tags["project"], "io-foo")
	}
}

func TestRedisInstanceRecoversLocationFromAssetNameWhenFieldEmpty(t *testing.T) {
	t.Parallel()
	d := newRedisInstanceDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//redis.googleapis.com/projects/real-proj/locations/us-west1/instances/io-foo-cache",
			AssetType: "redis.googleapis.com/Instance",
		},
		"real-proj")
	if got.Identity.Location != "us-west1" {
		t.Errorf("Location=%q, want us-west1 (recovered from asset name)", got.Identity.Location)
	}
}

func TestRedisInstanceDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newRedisInstanceDiscoverer()
	cases := []struct {
		name, in, wantName, wantLoc, wantImportID string
		wantErr                                   error
	}{
		{name: "asset name", in: "//redis.googleapis.com/projects/p/locations/us-east1/instances/cache1", wantName: "cache1", wantLoc: "us-east1", wantImportID: "projects/real-proj/locations/us-east1/instances/cache1"},
		{name: "import id", in: "projects/p/locations/us-central1/instances/cache1", wantName: "cache1", wantLoc: "us-central1", wantImportID: "projects/real-proj/locations/us-central1/instances/cache1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (location required)", in: "cache1", wantErr: ErrNotSupported},
		{name: "missing locations segment", in: "projects/p/instances/cache1", wantErr: ErrNotSupported},
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
			if got.Identity.ImportID != tc.wantImportID {
				t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, tc.wantImportID)
			}
		})
	}
}
