package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestVertexAIDatasetFromAsset(t *testing.T) {
	t.Parallel()
	d := newVertexAIDatasetDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//aiplatform.googleapis.com/projects/real-proj/locations/us-central1/datasets/1234567890",
			AssetType: "aiplatform.googleapis.com/Dataset",
			Project:   "real-proj",
			Location:  "us-central1",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_vertex_ai_dataset" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "1234567890" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/locations/us-central1/datasets/1234567890"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q, want us-central1", got.Identity.Location)
	}
	// ScopeStyleLabels relies on a.Labels flowing through to Tags so
	// the server-side labels.project filter attribution is preserved.
	if got.Identity.Tags["project"] != "io-foo" {
		t.Errorf("Tags[project]=%q, want %q", got.Identity.Tags["project"], "io-foo")
	}
}

func TestVertexAIDatasetDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newVertexAIDatasetDiscoverer()
	cases := []struct {
		name, in, wantID, wantLoc, wantImportID string
		wantErr                                 error
	}{
		{name: "asset name", in: "//aiplatform.googleapis.com/projects/p/locations/us-east1/datasets/42", wantID: "42", wantLoc: "us-east1", wantImportID: "projects/real-proj/locations/us-east1/datasets/42"},
		{name: "import id", in: "projects/p/locations/us-central1/datasets/42", wantID: "42", wantLoc: "us-central1", wantImportID: "projects/real-proj/locations/us-central1/datasets/42"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare id rejected (location required)", in: "42", wantErr: ErrNotSupported},
		{name: "missing locations segment", in: "projects/p/datasets/42", wantErr: ErrNotSupported},
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
			if got.Identity.NameHint != tc.wantID {
				t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, tc.wantID)
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
