package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	identitytoolkitv2 "google.golang.org/api/identitytoolkit/v2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func defaultSupportedIdpConfigIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:     "gcp",
		Type:      "google_identity_platform_default_supported_idp_config",
		NameHint:  "google_com",
		Address:   "google_identity_platform_default_supported_idp_config.google_com",
		ImportID:  "projects/my-project/defaultSupportedIdpConfigs/google.com",
		ProjectID: "my-project",
		NativeIDs: map[string]string{
			"idp_id":  "google.com",
			"enabled": "true",
		},
	}
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	e := newIdentityPlatformDefaultSupportedIdpConfigEnricher()
	assert.Equal(t, "google_identity_platform_default_supported_idp_config", e.ResourceType())
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newIdentityPlatformDefaultSupportedIdpConfigEnricher()
	ir := &imported.ImportedResource{Identity: defaultSupportedIdpConfigIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := identityPlatformDefaultSupportedIdpConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound}
		},
	}
	ir := &imported.ImportedResource{Identity: defaultSupportedIdpConfigIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrich_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 forbidden")
	e := identityPlatformDefaultSupportedIdpConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: defaultSupportedIdpConfigIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.NotErrorIs(t, err, ErrNotFound, "non-404 must not be reported as ErrNotFound")
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrich_HappyPath(t *testing.T) {
	t.Parallel()
	src := &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig{
		Name:         "projects/my-project/defaultSupportedIdpConfigs/google.com",
		Enabled:      true,
		ClientId:     "12345.apps.googleusercontent.com",
		ClientSecret: "supersecret",
	}
	var gotName string
	e := identityPlatformDefaultSupportedIdpConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, name string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
			gotName = name
			return src, nil
		},
	}
	ir := &imported.ImportedResource{Identity: defaultSupportedIdpConfigIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}}))
	assert.Equal(t, "projects/my-project/defaultSupportedIdpConfigs/google.com", gotName,
		"fetch must receive the recomposed full resource name")

	decoded, err := generated.UnmarshalAttrs("google_identity_platform_default_supported_idp_config", ir.Attrs)
	require.NoError(t, err)
	v, ok := decoded.(*generated.GoogleIdentityPlatformDefaultSupportedIdpConfig)
	require.True(t, ok)
	require.NotNil(t, v.IdpID)
	assert.Equal(t, "google.com", *v.IdpID.Literal)
	require.NotNil(t, v.Enabled)
	assert.True(t, *v.Enabled.Literal)
	require.NotNil(t, v.ClientID)
	assert.Equal(t, "12345.apps.googleusercontent.com", *v.ClientID.Literal)
	require.NotNil(t, v.ClientSecret)
	assert.Equal(t, "supersecret", *v.ClientSecret.Literal)
	require.NotNil(t, v.Project)
	assert.Equal(t, "my-project", *v.Project.Literal)

	// Computed-only fields must not leak into Attrs.
	assert.Nil(t, v.Name, "name is Computed-only")
	assert.Nil(t, v.ID)
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrich_DisabledStateMapsToEnabledFalse(t *testing.T) {
	t.Parallel()
	src := &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig{
		Name:    "projects/my-project/defaultSupportedIdpConfigs/facebook.com",
		Enabled: false,
	}
	e := identityPlatformDefaultSupportedIdpConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
			return src, nil
		},
	}
	ir := &imported.ImportedResource{Identity: defaultSupportedIdpConfigIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}}))
	decoded, err := generated.UnmarshalAttrs("google_identity_platform_default_supported_idp_config", ir.Attrs)
	require.NoError(t, err)
	v := decoded.(*generated.GoogleIdentityPlatformDefaultSupportedIdpConfig)
	require.NotNil(t, v.Enabled)
	assert.False(t, *v.Enabled.Literal, "Enabled=false → enabled=false (the curated drift hook)")
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrich_FallsBackToImportIDWhenNativeIDsMissing(t *testing.T) {
	t.Parallel()
	src := &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig{
		Name:    "projects/my-project/defaultSupportedIdpConfigs/apple.com",
		Enabled: true,
	}
	var gotName string
	e := identityPlatformDefaultSupportedIdpConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, name string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
			gotName = name
			return src, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_identity_platform_default_supported_idp_config",
			ImportID: "projects/my-project/defaultSupportedIdpConfigs/apple.com",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}}))
	assert.Equal(t, "projects/my-project/defaultSupportedIdpConfigs/apple.com", gotName)
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := identityPlatformDefaultSupportedIdpConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_identity_platform_default_supported_idp_config"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive resource name")
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	src := &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig{
		Name:    "projects/my-project/defaultSupportedIdpConfigs/google.com",
		Enabled: true,
	}
	e := identityPlatformDefaultSupportedIdpConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
			return src, nil
		},
	}
	id := defaultSupportedIdpConfigIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &probe))
	assert.Contains(t, probe, "idp_id")
	assert.Contains(t, probe, "enabled")
	assert.Contains(t, probe, "project")
}

func TestIdentityPlatformDefaultSupportedIdpConfigEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newIdentityPlatformDefaultSupportedIdpConfigEnricher().(*identityPlatformDefaultSupportedIdpConfigEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestIdentityPlatformDefaultSupportedIdpConfigRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_identity_platform_default_supported_idp_config"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_identity_platform_default_supported_idp_config", enr.ResourceType())
}
