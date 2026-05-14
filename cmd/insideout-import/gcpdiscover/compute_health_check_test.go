package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Bundle G2 (#473) — google_compute_health_check.

func TestComputeHealthCheckFromAsset_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeHealthCheckDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/healthChecks/io-foo-hc",
			AssetType: "compute.googleapis.com/HealthCheck",
			Project:   "real-proj",
		},
		"real-proj")
	assert.Equal(t, "google_compute_health_check", got.Identity.Type)
	assert.Equal(t, "io-foo-hc", got.Identity.NameHint)
	assert.Equal(t, "projects/real-proj/global/healthChecks/io-foo-hc", got.Identity.ImportID)
	assert.Equal(t, "real-proj", got.Identity.ProjectID)
	assert.Equal(t, "", got.Identity.Location)
	assert.Equal(t,
		"//compute.googleapis.com/projects/real-proj/global/healthChecks/io-foo-hc",
		got.Identity.NativeIDs["asset_name"])
}

func TestComputeHealthCheckDiscoverByID_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeHealthCheckDiscoverer()
	r, err := d.DiscoverByID(context.Background(), nil, "projects/p/global/healthChecks/hc1", "p")
	assert.NoError(t, err)
	assert.Equal(t, "google_compute_health_check", r.Identity.Type)
	assert.Equal(t, "hc1", r.Identity.NameHint)
	assert.Equal(t, "projects/p/global/healthChecks/hc1", r.Identity.ImportID)
}

func TestComputeHealthCheckDiscoverByID_UnrecognizedID(t *testing.T) {
	t.Parallel()
	d := newComputeHealthCheckDiscoverer()
	_, err := d.DiscoverByID(context.Background(), nil, "arn:aws:elb:::lb/x", "p")
	assert.True(t, errors.Is(err, ErrNotSupported), "want ErrNotSupported, got %v", err)
}

// TestComputeHealthCheckImportID pins the import-ID composition against
// the terraform-provider-google v6.x documented shape:
// projects/{{project}}/global/healthChecks/{{name}}.
func TestComputeHealthCheckImportID(t *testing.T) {
	t.Parallel()
	d := newComputeHealthCheckDiscoverer()
	cases := []struct {
		name, in, wantName, wantImportID string
		wantErr                          error
	}{
		{
			name:         "asset name",
			in:           "//compute.googleapis.com/projects/p/global/healthChecks/hc1",
			wantName:     "hc1",
			wantImportID: "projects/p/global/healthChecks/hc1",
		},
		{
			name:         "import id",
			in:           "projects/p/global/healthChecks/hc1",
			wantName:     "hc1",
			wantImportID: "projects/p/global/healthChecks/hc1",
		},
		{
			name:         "bare name",
			in:           "hc1",
			wantName:     "hc1",
			wantImportID: "projects/p/global/healthChecks/hc1",
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
