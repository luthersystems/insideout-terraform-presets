package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestCloudFunctions2FunctionFromAsset(t *testing.T) {
	t.Parallel()
	d := newCloudFunctions2FunctionDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//cloudfunctions.googleapis.com/projects/real-proj/locations/us-central1/functions/io-foo-fn",
			AssetType: "cloudfunctions.googleapis.com/Function",
			Project:   "real-proj",
			Location:  "us-central1",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_cloudfunctions2_function" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-fn" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/functions/io-foo-fn"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
}

func TestCloudFunctions2FunctionDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newCloudFunctions2FunctionDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//cloudfunctions.googleapis.com/projects/p/locations/us-central1/functions/fn1", wantName: "fn1"},
		{name: "import id", in: "projects/p/locations/us-central1/functions/fn1", wantName: "fn1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected", in: "fn1", wantErr: ErrNotSupported},
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
		})
	}
}
