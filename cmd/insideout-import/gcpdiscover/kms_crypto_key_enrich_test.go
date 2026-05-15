package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

var (
	_ AttributeEnricher = (*kmsCryptoKeyEnricher)(nil)
	_ ByIDEnricher      = (*kmsCryptoKeyEnricher)(nil)
)

func kmsCryptoKeyIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_kms_crypto_key",
		NameHint: "io-foo-key",
		Address:  "google_kms_crypto_key.io_foo_key",
		ImportID: "projects/my-project/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/io-foo-key",
		Location: "us-central1",
		NativeIDs: map[string]string{
			"asset_name": "//cloudkms.googleapis.com/projects/my-project/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/io-foo-key",
			"key_ring":   "io-foo-ring",
		},
	}
}

func TestMapKMSCryptoKey_Minimal(t *testing.T) {
	t.Parallel()
	src := &cloudkms.CryptoKey{
		Purpose: "ENCRYPT_DECRYPT",
	}
	got := mapKMSCryptoKey(src, "my-project", "us-central1", "io-foo-ring", "io-foo-key")
	require.NotNil(t, got.Name)
	assert.Equal(t, "io-foo-key", *got.Name.Literal)
	require.NotNil(t, got.KeyRing)
	assert.Equal(t, "projects/my-project/locations/us-central1/keyRings/io-foo-ring", *got.KeyRing.Literal)
	require.NotNil(t, got.Purpose)
	assert.Equal(t, "ENCRYPT_DECRYPT", *got.Purpose.Literal)
	assert.Nil(t, got.RotationPeriod)
	assert.Nil(t, got.ImportOnly)
	assert.Empty(t, got.VersionTemplate, "no version_template fields set must not emit empty block")
	assert.Empty(t, got.Labels)
}

func TestMapKMSCryptoKey_FullyPopulated(t *testing.T) {
	t.Parallel()
	src := &cloudkms.CryptoKey{
		Purpose:                  "ENCRYPT_DECRYPT",
		RotationPeriod:           "7776000s",
		DestroyScheduledDuration: "86400s",
		ImportOnly:               true,
		CryptoKeyBackend:         "projects/my-project/locations/us-central1/ekmConnections/my-ekm",
		Labels: map[string]string{
			"env":             "prod",
			"goog-managed-by": "ignored",
			"team":            "platform",
		},
		VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{
			Algorithm:       "GOOGLE_SYMMETRIC_ENCRYPTION",
			ProtectionLevel: "SOFTWARE",
		},
	}
	got := mapKMSCryptoKey(src, "my-project", "us-central1", "io-foo-ring", "io-foo-key")

	require.NotNil(t, got.RotationPeriod)
	assert.Equal(t, "7776000s", *got.RotationPeriod.Literal)
	require.NotNil(t, got.DestroyScheduledDuration)
	assert.Equal(t, "86400s", *got.DestroyScheduledDuration.Literal)
	require.NotNil(t, got.ImportOnly)
	assert.True(t, *got.ImportOnly.Literal)
	require.NotNil(t, got.CryptoKeyBackend)

	// Labels filtered.
	require.NotNil(t, got.Labels)
	assert.Equal(t, 2, len(got.Labels), "goog-* label must be filtered")
	assert.Contains(t, got.Labels, "env")
	assert.Contains(t, got.Labels, "team")
	assert.NotContains(t, got.Labels, "goog-managed-by")

	// VersionTemplate emitted as block.
	require.Len(t, got.VersionTemplate, 1)
	require.NotNil(t, got.VersionTemplate[0].Algorithm)
	assert.Equal(t, "GOOGLE_SYMMETRIC_ENCRYPTION", *got.VersionTemplate[0].Algorithm.Literal)
	require.NotNil(t, got.VersionTemplate[0].ProtectionLevel)
	assert.Equal(t, "SOFTWARE", *got.VersionTemplate[0].ProtectionLevel.Literal)
}

func TestKMSCryptoKeyEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newKMSCryptoKeyEnricher()
	ir := &imported.ImportedResource{Identity: kmsCryptoKeyIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{KMS: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestKMSCryptoKeyEnrich_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := kmsCryptoKeyEnricher{
		fetch: func(_ context.Context, _ *cloudkms.Service, _ string) (*cloudkms.CryptoKey, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: kmsCryptoKeyIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{KMS: &cloudkms.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ProjectID required")
}

func TestKMSCryptoKeyEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := kmsCryptoKeyEnricher{
		fetch: func(_ context.Context, _ *cloudkms.Service, _ string) (*cloudkms.CryptoKey, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_kms_crypto_key"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{KMS: &cloudkms.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive resource name")
}

func TestKMSCryptoKeyEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := kmsCryptoKeyEnricher{
		fetch: func(_ context.Context, _ *cloudkms.Service, _ string) (*cloudkms.CryptoKey, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "key not found"}
		},
	}
	ir := &imported.ImportedResource{Identity: kmsCryptoKeyIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{KMS: &cloudkms.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestKMSCryptoKeyEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 permission denied")
	e := kmsCryptoKeyEnricher{
		fetch: func(_ context.Context, _ *cloudkms.Service, _ string) (*cloudkms.CryptoKey, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: kmsCryptoKeyIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{KMS: &cloudkms.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.NotErrorIs(t, err, ErrNotFound, "non-404 must not collapse to ErrNotFound")
}

func TestKMSCryptoKeyEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	k := &cloudkms.CryptoKey{
		Purpose:        "ENCRYPT_DECRYPT",
		RotationPeriod: "7776000s",
		VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{
			Algorithm:       "GOOGLE_SYMMETRIC_ENCRYPTION",
			ProtectionLevel: "SOFTWARE",
		},
	}
	var gotName string
	e := kmsCryptoKeyEnricher{
		fetch: func(_ context.Context, _ *cloudkms.Service, name string) (*cloudkms.CryptoKey, error) {
			gotName = name
			return k, nil
		},
	}
	ir := &imported.ImportedResource{Identity: kmsCryptoKeyIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{KMS: &cloudkms.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "projects/my-project/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/io-foo-key", gotName)

	decoded, err := generated.UnmarshalAttrs("google_kms_crypto_key", ir.Attrs)
	require.NoError(t, err)
	gk, ok := decoded.(*generated.GoogleKMSCryptoKey)
	require.True(t, ok)
	require.NotNil(t, gk.Name)
	assert.Equal(t, "io-foo-key", *gk.Name.Literal)
	require.NotNil(t, gk.RotationPeriod)
	assert.Equal(t, "7776000s", *gk.RotationPeriod.Literal)
	require.Len(t, gk.VersionTemplate, 1)
}

func TestKMSCryptoKeyEnrichByID(t *testing.T) {
	t.Parallel()
	k := &cloudkms.CryptoKey{Purpose: "ENCRYPT_DECRYPT"}
	e := kmsCryptoKeyEnricher{
		fetch: func(_ context.Context, _ *cloudkms.Service, _ string) (*cloudkms.CryptoKey, error) {
			return k, nil
		},
	}
	id := kmsCryptoKeyIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{KMS: &cloudkms.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &payload))
	assert.Contains(t, payload, "name")
	assert.Contains(t, payload, "purpose")
}

func TestKMSCryptoKeyEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newKMSCryptoKeyEnricher().(*kmsCryptoKeyEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{KMS: &cloudkms.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestKMSCryptoKeyRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_kms_crypto_key"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_kms_crypto_key", enr.ResourceType())
}
