package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestCloudbuildTriggerFromAsset_Regional(t *testing.T) {
	t.Parallel()
	d := newCloudbuildTriggerDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//cloudbuild.googleapis.com/projects/real-proj/locations/us-central1/triggers/io-foo-trigger-abc",
			AssetType: "cloudbuild.googleapis.com/BuildTrigger",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_cloudbuild_trigger" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-trigger-abc" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/triggers/io-foo-trigger-abc"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
}

func TestCloudbuildTriggerRecoversLocationFromAssetNameWhenFieldEmpty(t *testing.T) {
	t.Parallel()
	d := newCloudbuildTriggerDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//cloudbuild.googleapis.com/projects/real-proj/locations/us-west1/triggers/io-foo-trigger-abc",
			AssetType: "cloudbuild.googleapis.com/BuildTrigger",
		},
		"real-proj")
	if got.Identity.Location != "us-west1" {
		t.Errorf("Location=%q, want us-west1 (recovered from asset name)", got.Identity.Location)
	}
	wantImport := "projects/real-proj/locations/us-west1/triggers/io-foo-trigger-abc"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q (regional shape from recovered location)", got.Identity.ImportID, wantImport)
	}
}

// TestCloudbuildTriggerFromAsset_Global pins the legacy global-scoped
// asset path: the provider still emits triggers without a /locations/
// segment for accounts that haven't opted into regional triggers. The
// import-ID shape and Identity.Location must match (both global).
func TestCloudbuildTriggerFromAsset_Global(t *testing.T) {
	t.Parallel()
	d := newCloudbuildTriggerDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//cloudbuild.googleapis.com/projects/real-proj/triggers/io-foo-trigger-abc",
			AssetType: "cloudbuild.googleapis.com/BuildTrigger",
			Project:   "real-proj",
		},
		"real-proj")
	wantImport := "projects/real-proj/triggers/io-foo-trigger-abc"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q (legacy global)", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty for legacy global", got.Identity.Location)
	}
}

func TestCloudbuildTriggerDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newCloudbuildTriggerDiscoverer()
	cases := []struct {
		name, in, wantName, wantLoc, wantImportID string
		wantErr                                   error
	}{
		{name: "regional asset name", in: "//cloudbuild.googleapis.com/projects/p/locations/us-east1/triggers/t1", wantName: "t1", wantLoc: "us-east1", wantImportID: "projects/real-proj/locations/us-east1/triggers/t1"},
		{name: "regional import id", in: "projects/p/locations/us-central1/triggers/t1", wantName: "t1", wantLoc: "us-central1", wantImportID: "projects/real-proj/locations/us-central1/triggers/t1"},
		{name: "legacy global asset name", in: "//cloudbuild.googleapis.com/projects/p/triggers/t1", wantName: "t1", wantLoc: "", wantImportID: "projects/real-proj/triggers/t1"},
		{name: "legacy global import id", in: "projects/p/triggers/t1", wantName: "t1", wantLoc: "", wantImportID: "projects/real-proj/triggers/t1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "missing triggers marker", in: "projects/p/builds/abc", wantErr: ErrNotSupported},
		{name: "empty name (trailing slash)", in: "projects/p/triggers/", wantErr: ErrNotSupported},
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
