package gcpdiscover

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	identitytoolkitv2 "google.golang.org/api/identitytoolkit/v2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestIdentityPlatformConfigEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_identity_platform_config", newIdentityPlatformConfigEnricher().ResourceType())
}

func TestIdentityPlatformConfigEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newIdentityPlatformConfigEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: identityPlatformConfigTFType, ImportID: "my-project"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: nil, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestIdentityPlatformConfigEnricher_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := &identityPlatformConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: identityPlatformConfigTFType},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ProjectID required")
}

func TestIdentityPlatformConfigEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &identityPlatformConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{Type: identityPlatformConfigTFType, ImportID: "my-project"}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestIdentityPlatformConfigEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	cfg := &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config{
		AuthorizedDomains:        []string{"example.com", "myapp.dev"},
		AutodeleteAnonymousUsers: true,
		MultiTenant: &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2MultiTenantConfig{
			AllowTenants:          true,
			DefaultTenantLocation: "organizations/123",
		},
		Monitoring: &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2MonitoringConfig{
			RequestLogging: &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2RequestLogging{Enabled: true},
		},
		SignIn: &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2SignInConfig{
			AllowDuplicateEmails: true,
			Anonymous:            &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Anonymous{Enabled: true},
			Email: &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Email{
				Enabled: true, PasswordRequired: true,
			},
			PhoneNumber: &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2PhoneNumber{Enabled: true},
		},
	}
	var gotName string
	e := &identityPlatformConfigEnricher{
		fetch: func(_ context.Context, _ *identitytoolkitv2.Service, name string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, error) {
			gotName = name
			return cfg, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: identityPlatformConfigTFType, ImportID: "my-project"},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "projects/my-project/config", gotName)

	decoded, err := generated.UnmarshalAttrs("google_identity_platform_config", ir.Attrs)
	require.NoError(t, err)
	ic, ok := decoded.(*generated.GoogleIdentityPlatformConfig)
	require.True(t, ok)
	require.NotNil(t, ic.AutodeleteAnonymousUsers)
	assert.True(t, *ic.AutodeleteAnonymousUsers.Literal)
	require.Len(t, ic.AuthorizedDomains, 2)
	require.Len(t, ic.MultiTenant, 1)
	require.NotNil(t, ic.MultiTenant[0].AllowTenants)
	assert.True(t, *ic.MultiTenant[0].AllowTenants.Literal)
	require.Len(t, ic.Monitoring, 1)
	require.Len(t, ic.Monitoring[0].RequestLogging, 1)
	require.NotNil(t, ic.Monitoring[0].RequestLogging[0].Enabled)
	assert.True(t, *ic.Monitoring[0].RequestLogging[0].Enabled.Literal)
	require.Len(t, ic.SignIn, 1)
	require.NotNil(t, ic.SignIn[0].AllowDuplicateEmails)
	require.Len(t, ic.SignIn[0].Anonymous, 1)
	require.Len(t, ic.SignIn[0].Email, 1)
	require.NotNil(t, ic.SignIn[0].Email[0].PasswordRequired)
}

func TestIdentityPlatformConfigEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	cfg := &identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config{
		AutodeleteAnonymousUsers: true,
	}
	mkFetch := func() func(context.Context, *identitytoolkitv2.Service, string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, error) {
		return func(_ context.Context, _ *identitytoolkitv2.Service, _ string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, error) {
			return cfg, nil
		}
	}
	enrichE := &identityPlatformConfigEnricher{fetch: mkFetch()}
	byIDE := &identityPlatformConfigEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{Type: identityPlatformConfigTFType, ImportID: "p"}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}, ProjectID: "p"}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestIdentityPlatformConfigEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newIdentityPlatformConfigEnricher().(*identityPlatformConfigEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{IdentityToolkit: &identitytoolkitv2.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestMapIdentityPlatformConfig_NilSafe(t *testing.T) {
	t.Parallel()
	got := mapIdentityPlatformConfig(nil, "p")
	require.NotNil(t, got)
	require.NotNil(t, got.Project)
	assert.Equal(t, "p", *got.Project.Literal)
}
