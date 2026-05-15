package gcpdiscover

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	sqladminv1 "google.golang.org/api/sqladmin/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestSQLUserEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_sql_user", newSQLUserEnricher().ResourceType())
}

func TestSQLUserEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newSQLUserEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      sqlUserTFType,
			NameHint:  "appuser",
			NativeIDs: map[string]string{"instance": "db1"},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestSQLUserEnricher_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := &sqlUserEnricher{
		fetch: func(_ context.Context, _ *sqladminv1.Service, _, _, _, _ string) (*sqladminv1.User, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: sqlUserTFType, NameHint: "appuser",
			NativeIDs: map[string]string{"instance": "db1"},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladminv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ProjectID required")
}

func TestSQLUserEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &sqlUserEnricher{
		fetch: func(_ context.Context, _ *sqladminv1.Service, _, _, _, _ string) (*sqladminv1.User, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{
		Type: sqlUserTFType, NameHint: "appuser",
		NativeIDs: map[string]string{"instance": "db1"},
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestSQLUserEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	user := &sqladminv1.User{
		Host: "%",
		Type: "BUILT_IN",
		// Password should NOT propagate even when set on the API
		// (it never does in practice, but defend in depth).
		Password: "secret",
	}
	e := &sqlUserEnricher{
		fetch: func(_ context.Context, _ *sqladminv1.Service, project, instance, host, name string) (*sqladminv1.User, error) {
			assert.Equal(t, "my-project", project)
			assert.Equal(t, "db1", instance)
			assert.Equal(t, "appuser", name)
			// No NativeIDs.host or ImportID host segment provided →
			// fetch host stays empty.
			assert.Empty(t, host)
			return user, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: sqlUserTFType, NameHint: "appuser",
			NativeIDs: map[string]string{"instance": "db1"},
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "my-project"}))

	decoded, err := generated.UnmarshalAttrs("google_sql_user", ir.Attrs)
	require.NoError(t, err)
	su, ok := decoded.(*generated.GoogleSqlUser)
	require.True(t, ok)
	require.NotNil(t, su.Name)
	assert.Equal(t, "appuser", *su.Name.Literal)
	require.NotNil(t, su.Instance)
	assert.Equal(t, "db1", *su.Instance.Literal)
	require.NotNil(t, su.Project)
	assert.Equal(t, "my-project", *su.Project.Literal)
	require.NotNil(t, su.Host)
	assert.Equal(t, "%", *su.Host.Literal)
	require.NotNil(t, su.Type_)
	assert.Equal(t, "BUILT_IN", *su.Type_.Literal)
	assert.Nil(t, su.Password, "password must be stripped on enrich")
}

func TestSQLUserEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	user := &sqladminv1.User{Host: "%", Type: "BUILT_IN"}
	mkFetch := func() func(context.Context, *sqladminv1.Service, string, string, string, string) (*sqladminv1.User, error) {
		return func(_ context.Context, _ *sqladminv1.Service, _, _, _, _ string) (*sqladminv1.User, error) {
			return user, nil
		}
	}
	enrichE := &sqlUserEnricher{fetch: mkFetch()}
	byIDE := &sqlUserEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{
		Type: sqlUserTFType, NameHint: "appuser",
		NativeIDs: map[string]string{"instance": "db1"},
	}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "p"}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestSQLUserEnricher_DerivesFromImportID(t *testing.T) {
	t.Parallel()
	var gotInstance, gotHost, gotName string
	e := &sqlUserEnricher{
		fetch: func(_ context.Context, _ *sqladminv1.Service, _, instance, host, name string) (*sqladminv1.User, error) {
			gotInstance, gotHost, gotName = instance, host, name
			return &sqladminv1.User{}, nil
		},
	}
	for _, tc := range []struct {
		name     string
		importID string
		wantInst string
		wantHost string
		wantUser string
	}{
		{"instance/host/name", "db1/%/appuser", "db1", "%", "appuser"},
		{"instance/name", "db1/appuser", "db1", "", "appuser"},
		{"instance/specifichost/name", "db1/10.0.0.1/appuser", "db1", "10.0.0.1", "appuser"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotInstance, gotHost, gotName = "", "", ""
			id := &imported.ResourceIdentity{
				Type:     sqlUserTFType,
				ImportID: tc.importID,
			}
			_, err := e.EnrichByID(context.Background(), id, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "p"})
			require.NoError(t, err)
			assert.Equal(t, tc.wantInst, gotInstance)
			assert.Equal(t, tc.wantHost, gotHost, "host must be preserved across the SDK call so users on different hosts don't collide")
			assert.Equal(t, tc.wantUser, gotName)
		})
	}
}

func TestParseSQLUserImportID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                            string
		in                              string
		wantInst, wantHost, wantName    string
	}{
		{"empty", "", "", "", ""},
		{"single segment treated as bare name", "appuser", "", "", "appuser"},
		{"two segments", "db1/appuser", "db1", "", "appuser"},
		{"three segments preserve host", "db1/%/appuser", "db1", "%", "appuser"},
		{"three segments specific host", "db1/10.0.0.1/appuser", "db1", "10.0.0.1", "appuser"},
		{"leading slash yields empty instance", "/appuser", "", "", "appuser"},
		{"trailing slash yields empty name", "db1/appuser/", "db1", "appuser", ""},
		{"three segments preserve embedded slashes via SplitN", "db1/host/name/with/extra", "db1", "host", "name/with/extra"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			instance, host, name := parseSQLUserImportID(tc.in)
			assert.Equal(t, tc.wantInst, instance)
			assert.Equal(t, tc.wantHost, host)
			assert.Equal(t, tc.wantName, name)
		})
	}
}

func TestSQLUserEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newSQLUserEnricher().(*sqlUserEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestSQLUserEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := &sqlUserEnricher{
		fetch: func(_ context.Context, _ *sqladminv1.Service, _, _, _, _ string) (*sqladminv1.User, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{Type: sqlUserTFType, NameHint: "u", NativeIDs: map[string]string{"instance": "db1"}}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
	assert.Equal(t, http.StatusForbidden, gerr.Code)
}

func TestMapSQLUser_SqlServerDetails(t *testing.T) {
	t.Parallel()
	got := mapSQLUser(&sqladminv1.User{
		SqlserverUserDetails: &sqladminv1.SqlServerUserDetails{
			Disabled:    true,
			ServerRoles: []string{"sysadmin", "dbcreator"},
		},
	}, "p", "db1", "u")
	require.Len(t, got.SqlServerUserDetails, 1)
	require.NotNil(t, got.SqlServerUserDetails[0].Disabled)
	assert.True(t, *got.SqlServerUserDetails[0].Disabled.Literal)
	require.Len(t, got.SqlServerUserDetails[0].ServerRoles, 2)
	assert.Equal(t, "sysadmin", *got.SqlServerUserDetails[0].ServerRoles[0].Literal)
}

func TestSQLUserEnricher_CannotDeriveInstanceOrName(t *testing.T) {
	t.Parallel()
	e := &sqlUserEnricher{
		fetch: func(_ context.Context, _ *sqladminv1.Service, _, _, _, _ string) (*sqladminv1.User, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: sqlUserTFType}}
	err := e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladminv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive instance/name")
}
