package gcpdiscover

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	monitoringv3 "google.golang.org/api/monitoring/v3"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestMonitoringAlertPolicyEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_monitoring_alert_policy", newMonitoringAlertPolicyEnricher().ResourceType())
}

func TestMonitoringAlertPolicyEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newMonitoringAlertPolicyEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: monitoringAlertPolicyTFType, ImportID: "projects/p/alertPolicies/1",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Monitoring: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestMonitoringAlertPolicyEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &monitoringAlertPolicyEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, _ string) (*monitoringv3.AlertPolicy, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{Type: monitoringAlertPolicyTFType, ImportID: "projects/p/alertPolicies/1"}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestMonitoringAlertPolicyEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	policy := &monitoringv3.AlertPolicy{
		DisplayName:          "CPU > 90%",
		Combiner:             "OR",
		Enabled:              true,
		Severity:             "WARNING",
		NotificationChannels: []string{"projects/p/notificationChannels/123"},
		UserLabels:           map[string]string{"team": "platform", "goog-managed": "true"},
		Documentation: &monitoringv3.Documentation{
			Content:  "Look at CPU dashboard",
			MimeType: "text/markdown",
			Subject:  "[ALERT] CPU",
			Links: []*monitoringv3.Link{
				{DisplayName: "Runbook", Url: "https://example.com/runbook"},
			},
		},
		AlertStrategy: &monitoringv3.AlertStrategy{
			AutoClose:             "1800s",
			NotificationPrompts:   []string{"OPENED"},
			NotificationRateLimit: &monitoringv3.NotificationRateLimit{Period: "300s"},
		},
		Conditions: []*monitoringv3.Condition{
			{DisplayName: "VM Instance - CPU utilization > 0.9"},
		},
	}
	var gotName string
	e := &monitoringAlertPolicyEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, n string) (*monitoringv3.AlertPolicy, error) {
			gotName = n
			return policy, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: monitoringAlertPolicyTFType, ImportID: "projects/my-project/alertPolicies/123",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "projects/my-project/alertPolicies/123", gotName)

	decoded, err := generated.UnmarshalAttrs("google_monitoring_alert_policy", ir.Attrs)
	require.NoError(t, err)
	ap, ok := decoded.(*generated.GoogleMonitoringAlertPolicy)
	require.True(t, ok)
	require.NotNil(t, ap.DisplayName)
	assert.Equal(t, "CPU > 90%", *ap.DisplayName.Literal)
	require.NotNil(t, ap.Combiner)
	assert.Equal(t, "OR", *ap.Combiner.Literal)
	require.NotNil(t, ap.Enabled)
	assert.True(t, *ap.Enabled.Literal)
	require.NotNil(t, ap.Severity)
	assert.Equal(t, "WARNING", *ap.Severity.Literal)
	require.Len(t, ap.NotificationChannels, 1)
	// goog-* filtered out of user_labels
	require.Len(t, ap.UserLabels, 1)
	_, hasGoog := ap.UserLabels["goog-managed"]
	assert.False(t, hasGoog)
	require.Len(t, ap.Documentation, 1)
	require.NotNil(t, ap.Documentation[0].Content)
	require.Len(t, ap.Documentation[0].Links, 1)
	require.NotNil(t, ap.Documentation[0].Links[0].URL)
	assert.Equal(t, "https://example.com/runbook", *ap.Documentation[0].Links[0].URL.Literal)
	require.Len(t, ap.AlertStrategy, 1)
	require.NotNil(t, ap.AlertStrategy[0].AutoClose)
	require.Len(t, ap.AlertStrategy[0].NotificationRateLimit, 1)
	require.Len(t, ap.Conditions, 1)
	require.NotNil(t, ap.Conditions[0].DisplayName)
}

func TestMonitoringAlertPolicyEnricher_NameFromHint(t *testing.T) {
	t.Parallel()
	var gotName string
	e := &monitoringAlertPolicyEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, n string) (*monitoringv3.AlertPolicy, error) {
			gotName = n
			return &monitoringv3.AlertPolicy{DisplayName: "x"}, nil
		},
	}
	id := &imported.ResourceIdentity{Type: monitoringAlertPolicyTFType, NameHint: "policy-abc"}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.Equal(t, "projects/p/alertPolicies/policy-abc", gotName)
}

func TestMonitoringAlertPolicyEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	policy := &monitoringv3.AlertPolicy{DisplayName: "x", Combiner: "OR"}
	mkFetch := func() func(context.Context, *monitoringv3.Service, string) (*monitoringv3.AlertPolicy, error) {
		return func(_ context.Context, _ *monitoringv3.Service, _ string) (*monitoringv3.AlertPolicy, error) {
			return policy, nil
		}
	}
	enrichE := &monitoringAlertPolicyEnricher{fetch: mkFetch()}
	byIDE := &monitoringAlertPolicyEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{Type: monitoringAlertPolicyTFType, ImportID: "projects/p/alertPolicies/1"}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestMonitoringAlertPolicyEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden}
	e := &monitoringAlertPolicyEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, _ string) (*monitoringv3.AlertPolicy, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{Type: monitoringAlertPolicyTFType, ImportID: "projects/p/alertPolicies/1"}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
}

func TestMonitoringAlertPolicyEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newMonitoringAlertPolicyEnricher().(*monitoringAlertPolicyEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestMonitoringAlertPolicyEnricher_CannotDeriveName(t *testing.T) {
	t.Parallel()
	e := &monitoringAlertPolicyEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, _ string) (*monitoringv3.AlertPolicy, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: monitoringAlertPolicyTFType}}
	err := e.Enrich(context.Background(), ir, EnrichClients{Monitoring: &monitoringv3.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive resource name")
}
