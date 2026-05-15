package gcpdiscover

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	monitoringv1 "google.golang.org/api/monitoring/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestMonitoringDashboardEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_monitoring_dashboard", newMonitoringDashboardEnricher().ResourceType())
}

func TestMonitoringDashboardEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newMonitoringDashboardEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: monitoringDashboardTFType, ImportID: "projects/p/dashboards/1"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{MonitoringDashboard: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestMonitoringDashboardEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &monitoringDashboardEnricher{
		fetch: func(_ context.Context, _ *monitoringv1.Service, _ string) (*monitoringv1.Dashboard, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{Type: monitoringDashboardTFType, ImportID: "projects/p/dashboards/1"}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{MonitoringDashboard: &monitoringv1.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestMonitoringDashboardEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	dashboard := &monitoringv1.Dashboard{
		DisplayName: "Prod Overview",
		Name:        "projects/my-project/dashboards/abc",
		Etag:        "etag-foo",
		Labels:      map[string]string{"team": "platform"},
		GridLayout: &monitoringv1.GridLayout{
			Columns: 12,
		},
	}
	var gotName string
	e := &monitoringDashboardEnricher{
		fetch: func(_ context.Context, _ *monitoringv1.Service, n string) (*monitoringv1.Dashboard, error) {
			gotName = n
			return dashboard, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: monitoringDashboardTFType, ImportID: "projects/my-project/dashboards/abc"},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{MonitoringDashboard: &monitoringv1.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "projects/my-project/dashboards/abc", gotName)

	decoded, err := generated.UnmarshalAttrs("google_monitoring_dashboard", ir.Attrs)
	require.NoError(t, err)
	d, ok := decoded.(*generated.GoogleMonitoringDashboard)
	require.True(t, ok)
	require.NotNil(t, d.Project)
	assert.Equal(t, "my-project", *d.Project.Literal)
	require.NotNil(t, d.DashboardJSON)
	// dashboard_json must contain displayName and not the stripped Name / Etag.
	js := *d.DashboardJSON.Literal
	assert.Contains(t, js, `"displayName":"Prod Overview"`)
	assert.NotContains(t, js, `"name":"projects/my-project/dashboards/abc"`)
	assert.NotContains(t, js, `"etag":"etag-foo"`)
	// Round-trip JSON parse.
	var roundTrip map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(js), &roundTrip))
	assert.Equal(t, "Prod Overview", roundTrip["displayName"])
}

func TestMonitoringDashboardEnricher_NameFromHint(t *testing.T) {
	t.Parallel()
	var gotName string
	e := &monitoringDashboardEnricher{
		fetch: func(_ context.Context, _ *monitoringv1.Service, n string) (*monitoringv1.Dashboard, error) {
			gotName = n
			return &monitoringv1.Dashboard{DisplayName: "x"}, nil
		},
	}
	id := &imported.ResourceIdentity{Type: monitoringDashboardTFType, NameHint: "dash-abc"}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{MonitoringDashboard: &monitoringv1.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.Equal(t, "projects/p/dashboards/dash-abc", gotName)
}

func TestMonitoringDashboardEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	dash := &monitoringv1.Dashboard{DisplayName: "x"}
	mkFetch := func() func(context.Context, *monitoringv1.Service, string) (*monitoringv1.Dashboard, error) {
		return func(_ context.Context, _ *monitoringv1.Service, _ string) (*monitoringv1.Dashboard, error) {
			return dash, nil
		}
	}
	enrichE := &monitoringDashboardEnricher{fetch: mkFetch()}
	byIDE := &monitoringDashboardEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{Type: monitoringDashboardTFType, ImportID: "projects/p/dashboards/1"}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{MonitoringDashboard: &monitoringv1.Service{}, ProjectID: "p"}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{MonitoringDashboard: &monitoringv1.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestMonitoringDashboardEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newMonitoringDashboardEnricher().(*monitoringDashboardEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{MonitoringDashboard: &monitoringv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestMonitoringDashboardEnricher_CannotDeriveName(t *testing.T) {
	t.Parallel()
	e := &monitoringDashboardEnricher{
		fetch: func(_ context.Context, _ *monitoringv1.Service, _ string) (*monitoringv1.Dashboard, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: monitoringDashboardTFType}}
	err := e.Enrich(context.Background(), ir, EnrichClients{MonitoringDashboard: &monitoringv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive resource name")
}
