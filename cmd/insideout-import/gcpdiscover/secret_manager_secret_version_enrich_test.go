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
	secretmanagerv1 "google.golang.org/api/secretmanager/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func secretVersionIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_secret_manager_secret_version",
		NameHint: "api-key-v1",
		Address:  "google_secret_manager_secret_version.api_key_v1",
		ImportID: "projects/my-project/secrets/api-key/versions/1",
		NativeIDs: map[string]string{
			"secret":  "projects/my-project/secrets/api-key",
			"version": "1",
			"state":   "ENABLED",
		},
	}
}

func TestSecretManagerSecretVersionEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	e := newSecretManagerSecretVersionEnricher()
	assert.Equal(t, "google_secret_manager_secret_version", e.ResourceType())
}

func TestSecretManagerSecretVersionEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newSecretManagerSecretVersionEnricher()
	ir := &imported.ImportedResource{Identity: secretVersionIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{SecretManager: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestSecretManagerSecretVersionEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := secretManagerSecretVersionEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.SecretVersion, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound}
		},
	}
	ir := &imported.ImportedResource{Identity: secretVersionIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSecretManagerSecretVersionEnrich_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 forbidden")
	e := secretManagerSecretVersionEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.SecretVersion, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: secretVersionIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.NotErrorIs(t, err, ErrNotFound, "non-404 must not be reported as ErrNotFound")
}

func TestSecretManagerSecretVersionEnrich_HappyPath(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.SecretVersion{
		Name:  "projects/my-project/secrets/api-key/versions/1",
		State: "ENABLED",
	}
	var gotName string
	e := secretManagerSecretVersionEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, name string) (*secretmanagerv1.SecretVersion, error) {
			gotName = name
			return src, nil
		},
	}
	ir := &imported.ImportedResource{Identity: secretVersionIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}}))
	assert.Equal(t, "projects/my-project/secrets/api-key/versions/1", gotName,
		"fetch must receive the recomposed full version name")

	decoded, err := generated.UnmarshalAttrs("google_secret_manager_secret_version", ir.Attrs)
	require.NoError(t, err)
	v, ok := decoded.(*generated.GoogleSecretManagerSecretVersion)
	require.True(t, ok)
	require.NotNil(t, v.Secret)
	assert.Equal(t, "projects/my-project/secrets/api-key", *v.Secret.Literal,
		"secret holds the parent secret path, not the version path")
	require.NotNil(t, v.Enabled)
	assert.True(t, *v.Enabled.Literal, "state=ENABLED → enabled=true")

	// Computed-only and Sensitive fields must not leak into Attrs.
	assert.Nil(t, v.Name, "name is Computed-only")
	assert.Nil(t, v.CreateTime)
	assert.Nil(t, v.DestroyTime)
	assert.Nil(t, v.Version)
	assert.Nil(t, v.SecretData, "secret payload must never leak — Get does not return it")
	assert.Nil(t, v.IsSecretDataBase64)
}

func TestSecretManagerSecretVersionEnrich_DisabledStateMapsToEnabledFalse(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.SecretVersion{
		Name:  "projects/my-project/secrets/api-key/versions/2",
		State: "DISABLED",
	}
	e := secretManagerSecretVersionEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.SecretVersion, error) {
			return src, nil
		},
	}
	ir := &imported.ImportedResource{Identity: secretVersionIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}}))
	decoded, err := generated.UnmarshalAttrs("google_secret_manager_secret_version", ir.Attrs)
	require.NoError(t, err)
	v := decoded.(*generated.GoogleSecretManagerSecretVersion)
	require.NotNil(t, v.Enabled)
	assert.False(t, *v.Enabled.Literal, "state=DISABLED → enabled=false (the curated mapping the policy gates drift on)")
}

func TestSecretManagerSecretVersionEnrich_FallsBackToImportIDWhenNativeIDsMissing(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.SecretVersion{
		Name:  "projects/my-project/secrets/api-key/versions/1",
		State: "ENABLED",
	}
	var gotName string
	e := secretManagerSecretVersionEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, name string) (*secretmanagerv1.SecretVersion, error) {
			gotName = name
			return src, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_secret_manager_secret_version",
			ImportID: "projects/my-project/secrets/api-key/versions/1",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}}))
	assert.Equal(t, "projects/my-project/secrets/api-key/versions/1", gotName)
}

func TestSecretManagerSecretVersionEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := secretManagerSecretVersionEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.SecretVersion, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_secret_manager_secret_version"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive version resource name")
}

func TestSecretManagerSecretVersionEnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.SecretVersion{
		Name:  "projects/my-project/secrets/api-key/versions/1",
		State: "ENABLED",
	}
	e := secretManagerSecretVersionEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.SecretVersion, error) {
			return src, nil
		},
	}
	id := secretVersionIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{SecretManager: &secretmanagerv1.Service{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &probe))
	assert.Contains(t, probe, "secret")
	assert.Contains(t, probe, "enabled")
}

func TestSecretManagerSecretVersionEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newSecretManagerSecretVersionEnricher().(*secretManagerSecretVersionEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{SecretManager: &secretmanagerv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestSecretManagerSecretVersionRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_secret_manager_secret_version"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_secret_manager_secret_version", enr.ResourceType())
}
