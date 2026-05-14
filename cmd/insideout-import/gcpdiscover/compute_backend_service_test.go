package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Bundle G2 (#473) — google_compute_backend_service.

func TestComputeBackendServiceFromAsset_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeBackendServiceDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/backendServices/io-foo-bs",
			AssetType: "compute.googleapis.com/BackendService",
			Project:   "real-proj",
		},
		"real-proj")
	assert.Equal(t, "google_compute_backend_service", got.Identity.Type)
	assert.Equal(t, "io-foo-bs", got.Identity.NameHint)
	assert.Equal(t, "projects/real-proj/global/backendServices/io-foo-bs", got.Identity.ImportID)
	assert.Equal(t, "real-proj", got.Identity.ProjectID)
	assert.Equal(t, "", got.Identity.Location)
	assert.Equal(t,
		"//compute.googleapis.com/projects/real-proj/global/backendServices/io-foo-bs",
		got.Identity.NativeIDs["asset_name"])
}

func TestComputeBackendServiceDiscoverByID_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeBackendServiceDiscoverer()
	r, err := d.DiscoverByID(context.Background(), nil, "projects/p/global/backendServices/bs1", "p")
	assert.NoError(t, err)
	assert.Equal(t, "google_compute_backend_service", r.Identity.Type)
	assert.Equal(t, "bs1", r.Identity.NameHint)
	assert.Equal(t, "projects/p/global/backendServices/bs1", r.Identity.ImportID)
}

func TestComputeBackendServiceDiscoverByID_UnrecognizedID(t *testing.T) {
	t.Parallel()
	d := newComputeBackendServiceDiscoverer()
	_, err := d.DiscoverByID(context.Background(), nil, "arn:aws:elb:::lb/x", "p")
	assert.True(t, errors.Is(err, ErrNotSupported), "want ErrNotSupported, got %v", err)
}

// TestComputeBackendServiceImportID pins the import-ID composition
// against the terraform-provider-google v6.x documented shape:
// projects/{{project}}/global/backendServices/{{name}}.
func TestComputeBackendServiceImportID(t *testing.T) {
	t.Parallel()
	d := newComputeBackendServiceDiscoverer()
	cases := []struct {
		name, in, wantName, wantImportID string
		wantErr                          error
	}{
		{
			name:         "asset name",
			in:           "//compute.googleapis.com/projects/p/global/backendServices/bs1",
			wantName:     "bs1",
			wantImportID: "projects/p/global/backendServices/bs1",
		},
		{
			name:         "import id",
			in:           "projects/p/global/backendServices/bs1",
			wantName:     "bs1",
			wantImportID: "projects/p/global/backendServices/bs1",
		},
		{
			name:         "bare name",
			in:           "bs1",
			wantName:     "bs1",
			wantImportID: "projects/p/global/backendServices/bs1",
		},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized shape", in: "arn:aws:elb:::lb/x", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := d.DiscoverByID(context.Background(), nil, tc.in, "p")
			if tc.wantErr != nil {
				assert.True(t, errors.Is(err, tc.wantErr), "err=%v, want %v", err, tc.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantName, r.Identity.NameHint)
			assert.Equal(t, tc.wantImportID, r.Identity.ImportID)
		})
	}
}
