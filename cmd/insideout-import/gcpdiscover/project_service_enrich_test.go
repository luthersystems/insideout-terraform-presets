package gcpdiscover

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	serviceusagev1 "google.golang.org/api/serviceusage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestProjectServiceEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_project_service", newProjectServiceEnricher().ResourceType())
}

func TestProjectServiceEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newProjectServiceEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      projectServiceTFType,
			NameHint:  "secretmanager.googleapis.com",
			ProjectID: "my-project",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{ServiceUsage: nil, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestProjectServiceEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &projectServiceEnricher{
		fetch: func(_ context.Context, _ *serviceusagev1.Service, _ string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{
		Type: projectServiceTFType, NameHint: "secretmanager.googleapis.com",
		ProjectID: "my-project",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceUsage: &serviceusagev1.Service{}, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestProjectServiceEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	apiSvc := &serviceusagev1.GoogleApiServiceusageV1Service{
		Name:  "projects/my-project/services/secretmanager.googleapis.com",
		State: "ENABLED",
	}
	e := &projectServiceEnricher{
		fetch: func(_ context.Context, _ *serviceusagev1.Service, name string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
			assert.Equal(t, "projects/my-project/services/secretmanager.googleapis.com", name)
			return apiSvc, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: projectServiceTFType, NameHint: "secretmanager.googleapis.com",
			ProjectID: "my-project",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{ServiceUsage: &serviceusagev1.Service{}, ProjectID: "my-project"}))

	decoded, err := generated.UnmarshalAttrs("google_project_service", ir.Attrs)
	require.NoError(t, err)
	ps, ok := decoded.(*generated.GoogleProjectService)
	require.True(t, ok)
	require.NotNil(t, ps.Project)
	assert.Equal(t, "my-project", *ps.Project.Literal)
	require.NotNil(t, ps.Service)
	assert.Equal(t, "secretmanager.googleapis.com", *ps.Service.Literal)
	// Lifecycle flags are user prefs with no API analogue — must stay nil.
	assert.Nil(t, ps.DisableOnDestroy)
	assert.Nil(t, ps.DisableDependentServices)
	// Computed-only `id` is not populated per decision #5.
	assert.Nil(t, ps.ID)
}

func TestProjectServiceEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	apiSvc := &serviceusagev1.GoogleApiServiceusageV1Service{State: "ENABLED"}
	mkFetch := func() func(context.Context, *serviceusagev1.Service, string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
		return func(_ context.Context, _ *serviceusagev1.Service, _ string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
			return apiSvc, nil
		}
	}
	enrichE := &projectServiceEnricher{fetch: mkFetch()}
	byIDE := &projectServiceEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{
		Type: projectServiceTFType, NameHint: "secretmanager.googleapis.com",
		ProjectID: "p",
	}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{ServiceUsage: &serviceusagev1.Service{}, ProjectID: "p"}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{ServiceUsage: &serviceusagev1.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestProjectServiceEnricher_DerivesFromImportID(t *testing.T) {
	t.Parallel()
	var gotName string
	e := &projectServiceEnricher{
		fetch: func(_ context.Context, _ *serviceusagev1.Service, name string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
			gotName = name
			return &serviceusagev1.GoogleApiServiceusageV1Service{}, nil
		},
	}
	// Only ImportID populated — derive project + service.
	id := &imported.ResourceIdentity{
		Type:     projectServiceTFType,
		ImportID: "their-project/pubsub.googleapis.com",
	}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceUsage: &serviceusagev1.Service{}})
	require.NoError(t, err)
	assert.Equal(t, "projects/their-project/services/pubsub.googleapis.com", gotName)
}

func TestProjectServiceEnricher_FallbackProject(t *testing.T) {
	t.Parallel()
	var gotName string
	e := &projectServiceEnricher{
		fetch: func(_ context.Context, _ *serviceusagev1.Service, name string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
			gotName = name
			return &serviceusagev1.GoogleApiServiceusageV1Service{}, nil
		},
	}
	// No Identity.ProjectID, no ImportID — fall back to EnrichClients.ProjectID.
	id := &imported.ResourceIdentity{
		Type:     projectServiceTFType,
		NameHint: "compute.googleapis.com",
	}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceUsage: &serviceusagev1.Service{}, ProjectID: "fallback-project"})
	require.NoError(t, err)
	assert.Equal(t, "projects/fallback-project/services/compute.googleapis.com", gotName)
}

func TestProjectServiceEnricher_CannotDeriveServiceOrProject(t *testing.T) {
	t.Parallel()
	e := &projectServiceEnricher{
		fetch: func(_ context.Context, _ *serviceusagev1.Service, _ string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	// Bare Identity — no project, no service, no ImportID.
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: projectServiceTFType}}
	err := e.Enrich(context.Background(), ir, EnrichClients{ServiceUsage: &serviceusagev1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive project/service")
}

func TestProjectServiceEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newProjectServiceEnricher().(*projectServiceEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{ServiceUsage: &serviceusagev1.Service{}})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestProjectServiceEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := &projectServiceEnricher{
		fetch: func(_ context.Context, _ *serviceusagev1.Service, _ string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{Type: projectServiceTFType, NameHint: "compute.googleapis.com", ProjectID: "p"}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceUsage: &serviceusagev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
	assert.Equal(t, http.StatusForbidden, gerr.Code)
}

func TestParseProjectServiceImportID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		in          string
		wantProject string
		wantService string
	}{
		{"empty", "", "", ""},
		{"single segment treated as bare service", "secretmanager.googleapis.com", "", "secretmanager.googleapis.com"},
		{"two segments", "my-project/secretmanager.googleapis.com", "my-project", "secretmanager.googleapis.com"},
		{"leading slash yields empty project", "/secretmanager.googleapis.com", "", "secretmanager.googleapis.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project, service := parseProjectServiceImportID(tc.in)
			assert.Equal(t, tc.wantProject, project)
			assert.Equal(t, tc.wantService, service)
		})
	}
}
