package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Bundle G2 (#473) — google_compute_managed_ssl_certificate.

func TestComputeManagedSSLCertificateFromAsset_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeManagedSSLCertificateDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/sslCertificates/io-foo-cert",
			AssetType: "compute.googleapis.com/SslCertificate",
			Project:   "real-proj",
		},
		"real-proj")
	assert.Equal(t, "google_compute_managed_ssl_certificate", got.Identity.Type)
	assert.Equal(t, "io-foo-cert", got.Identity.NameHint)
	assert.Equal(t, "projects/real-proj/global/sslCertificates/io-foo-cert", got.Identity.ImportID)
	assert.Equal(t, "real-proj", got.Identity.ProjectID)
	assert.Equal(t, "", got.Identity.Location)
	assert.Equal(t,
		"//compute.googleapis.com/projects/real-proj/global/sslCertificates/io-foo-cert",
		got.Identity.NativeIDs["asset_name"])
}

func TestComputeManagedSSLCertificateDiscoverByID_HappyPath(t *testing.T) {
	t.Parallel()
	d := newComputeManagedSSLCertificateDiscoverer()
	r, err := d.DiscoverByID(context.Background(), nil, "projects/p/global/sslCertificates/c1", "p")
	assert.NoError(t, err)
	assert.Equal(t, "google_compute_managed_ssl_certificate", r.Identity.Type)
	assert.Equal(t, "c1", r.Identity.NameHint)
	assert.Equal(t, "projects/p/global/sslCertificates/c1", r.Identity.ImportID)
}

func TestComputeManagedSSLCertificateDiscoverByID_UnrecognizedID(t *testing.T) {
	t.Parallel()
	d := newComputeManagedSSLCertificateDiscoverer()
	_, err := d.DiscoverByID(context.Background(), nil, "arn:aws:acm:::cert/x", "p")
	assert.True(t, errors.Is(err, ErrNotSupported), "want ErrNotSupported, got %v", err)
}

// TestComputeManagedSSLCertificateImportID pins the import-ID
// composition against the terraform-provider-google v6.x documented
// shape: projects/{{project}}/global/sslCertificates/{{name}}.
func TestComputeManagedSSLCertificateImportID(t *testing.T) {
	t.Parallel()
	d := newComputeManagedSSLCertificateDiscoverer()
	cases := []struct {
		name, in, wantName, wantImportID string
		wantErr                          error
	}{
		{
			name:         "asset name",
			in:           "//compute.googleapis.com/projects/p/global/sslCertificates/c1",
			wantName:     "c1",
			wantImportID: "projects/p/global/sslCertificates/c1",
		},
		{
			name:         "import id",
			in:           "projects/p/global/sslCertificates/c1",
			wantName:     "c1",
			wantImportID: "projects/p/global/sslCertificates/c1",
		},
		{
			name:         "bare name",
			in:           "c1",
			wantName:     "c1",
			wantImportID: "projects/p/global/sslCertificates/c1",
		},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized shape", in: "arn:aws:acm:::cert/x", wantErr: ErrNotSupported},
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
