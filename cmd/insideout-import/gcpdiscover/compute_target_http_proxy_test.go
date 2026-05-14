package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Bundle G2 (#473) — google_compute_target_http_proxy.

func TestComputeTargetHTTPProxyFromAsset_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeTargetHTTPProxyDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/targetHttpProxies/io-foo-proxy",
			AssetType: "compute.googleapis.com/TargetHttpProxy",
			Project:   "real-proj",
		},
		"real-proj")
	assert.Equal(t, "google_compute_target_http_proxy", got.Identity.Type)
	assert.Equal(t, "io-foo-proxy", got.Identity.NameHint)
	assert.Equal(t, "projects/real-proj/global/targetHttpProxies/io-foo-proxy", got.Identity.ImportID)
	assert.Equal(t, "real-proj", got.Identity.ProjectID)
	assert.Equal(t, "", got.Identity.Location)
	assert.Equal(t,
		"//compute.googleapis.com/projects/real-proj/global/targetHttpProxies/io-foo-proxy",
		got.Identity.NativeIDs["asset_name"])
}

func TestComputeTargetHTTPProxyDiscoverByID_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeTargetHTTPProxyDiscoverer()
	r, err := d.DiscoverByID(context.Background(), nil, "projects/p/global/targetHttpProxies/p1", "p")
	assert.NoError(t, err)
	assert.Equal(t, "google_compute_target_http_proxy", r.Identity.Type)
	assert.Equal(t, "p1", r.Identity.NameHint)
	assert.Equal(t, "projects/p/global/targetHttpProxies/p1", r.Identity.ImportID)
}

func TestComputeTargetHTTPProxyDiscoverByID_UnrecognizedID(t *testing.T) {
	t.Parallel()
	d := newComputeTargetHTTPProxyDiscoverer()
	_, err := d.DiscoverByID(context.Background(), nil, "arn:aws:elb:::lb/x", "p")
	assert.True(t, errors.Is(err, ErrNotSupported), "want ErrNotSupported, got %v", err)
}

// TestComputeTargetHTTPProxyImportID pins the import-ID composition
// against the terraform-provider-google v6.x documented shape:
// projects/{{project}}/global/targetHttpProxies/{{name}}.
func TestComputeTargetHTTPProxyImportID(t *testing.T) {
	t.Parallel()
	d := newComputeTargetHTTPProxyDiscoverer()
	cases := []struct {
		name, in, wantName, wantImportID string
		wantErr                          error
	}{
		{
			name:         "asset name",
			in:           "//compute.googleapis.com/projects/p/global/targetHttpProxies/p1",
			wantName:     "p1",
			wantImportID: "projects/p/global/targetHttpProxies/p1",
		},
		{
			name:         "import id",
			in:           "projects/p/global/targetHttpProxies/p1",
			wantName:     "p1",
			wantImportID: "projects/p/global/targetHttpProxies/p1",
		},
		{
			name:         "bare name",
			in:           "p1",
			wantName:     "p1",
			wantImportID: "projects/p/global/targetHttpProxies/p1",
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
