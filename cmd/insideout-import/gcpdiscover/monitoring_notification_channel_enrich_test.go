package gcpdiscover

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	monitoringv3 "google.golang.org/api/monitoring/v3"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestMonitoringNotificationChannelEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_monitoring_notification_channel", newMonitoringNotificationChannelEnricher().ResourceType())
}

func TestMonitoringNotificationChannelEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newMonitoringNotificationChannelEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: monitoringNotificationChannelTFType, ImportID: "projects/p/notificationChannels/1"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Monitoring: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestMonitoringNotificationChannelEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &monitoringNotificationChannelEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, _ string) (*monitoringv3.NotificationChannel, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{Type: monitoringNotificationChannelTFType, ImportID: "projects/p/notificationChannels/1"}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestMonitoringNotificationChannelEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	ch := &monitoringv3.NotificationChannel{
		DisplayName: "Pager",
		Description: "On-call team",
		Type:        "email",
		Enabled:     true,
		Labels:      map[string]string{"email_address": "oncall@example.com"},
		UserLabels:  map[string]string{"team": "platform", "goog-managed": "true", "goog_internal": "x"},
	}
	var gotName string
	e := &monitoringNotificationChannelEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, n string) (*monitoringv3.NotificationChannel, error) {
			gotName = n
			return ch, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: monitoringNotificationChannelTFType, ImportID: "projects/my-project/notificationChannels/123",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "projects/my-project/notificationChannels/123", gotName)

	decoded, err := generated.UnmarshalAttrs("google_monitoring_notification_channel", ir.Attrs)
	require.NoError(t, err)
	nc, ok := decoded.(*generated.GoogleMonitoringNotificationChannel)
	require.True(t, ok)
	require.NotNil(t, nc.DisplayName)
	assert.Equal(t, "Pager", *nc.DisplayName.Literal)
	require.NotNil(t, nc.Type_)
	assert.Equal(t, "email", *nc.Type_.Literal)
	require.NotNil(t, nc.Enabled)
	assert.True(t, *nc.Enabled.Literal)
	// `labels` map: functional config, no filtering.
	require.NotNil(t, nc.Labels)
	require.Contains(t, nc.Labels, "email_address")
	// user_labels: goog-* filtered.
	require.NotNil(t, nc.UserLabels)
	assert.Len(t, nc.UserLabels, 1)
	_, hasGoog := nc.UserLabels["goog-managed"]
	assert.False(t, hasGoog)
	_, hasGoogU := nc.UserLabels["goog_internal"]
	assert.False(t, hasGoogU)
}

func TestMonitoringNotificationChannelEnricher_NameFromHint(t *testing.T) {
	t.Parallel()
	var gotName string
	e := &monitoringNotificationChannelEnricher{
		fetch: func(_ context.Context, _ *monitoringv3.Service, n string) (*monitoringv3.NotificationChannel, error) {
			gotName = n
			return &monitoringv3.NotificationChannel{DisplayName: "x"}, nil
		},
	}
	id := &imported.ResourceIdentity{Type: monitoringNotificationChannelTFType, NameHint: "ch-abc"}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.Equal(t, "projects/p/notificationChannels/ch-abc", gotName)
}

func TestMonitoringNotificationChannelEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	ch := &monitoringv3.NotificationChannel{DisplayName: "x", Type: "email"}
	mkFetch := func() func(context.Context, *monitoringv3.Service, string) (*monitoringv3.NotificationChannel, error) {
		return func(_ context.Context, _ *monitoringv3.Service, _ string) (*monitoringv3.NotificationChannel, error) {
			return ch, nil
		}
	}
	enrichE := &monitoringNotificationChannelEnricher{fetch: mkFetch()}
	byIDE := &monitoringNotificationChannelEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{Type: monitoringNotificationChannelTFType, ImportID: "projects/p/notificationChannels/1"}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestMonitoringNotificationChannelEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newMonitoringNotificationChannelEnricher().(*monitoringNotificationChannelEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{Monitoring: &monitoringv3.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}
