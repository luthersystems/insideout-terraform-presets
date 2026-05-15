package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	monitoringv3 "google.golang.org/api/monitoring/v3"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// monitoringNotificationChannelEnricher implements AttributeEnricher
// AND ByIDEnricher for google_monitoring_notification_channel. Pairs
// with monitoringNotificationChannelDiscoverer.
//
// NotificationChannels.Get takes a single fully-qualified name of the
// form `projects/<p>/notificationChannels/<id>`. The discoverer puts
// that string in ImportID and the short id in NameHint.
//
// Mapping note on labels vs. user_labels: the TF schema has TWO maps
// — `labels` (the channel's configuration parameters, e.g. email_address
// for email channels, channel_name for slack) and `user_labels`
// (free-form organizing labels). Both come from the SDK as
// map[string]string. The goog-* prefix filter applies to user_labels
// only; the `labels` map carries the channel's functional configuration
// and must not be filtered.
//
// Computed-only fields skipped per decision #5: id, name, verification_status.
type monitoringNotificationChannelEnricher struct {
	fetch func(ctx context.Context, svc *monitoringv3.Service, name string) (*monitoringv3.NotificationChannel, error)
}

func newMonitoringNotificationChannelEnricher() AttributeEnricher {
	return &monitoringNotificationChannelEnricher{fetch: defaultMonitoringNotificationChannelFetch}
}

var (
	_ AttributeEnricher = (*monitoringNotificationChannelEnricher)(nil)
	_ ByIDEnricher      = (*monitoringNotificationChannelEnricher)(nil)
)

func (monitoringNotificationChannelEnricher) ResourceType() string {
	return monitoringNotificationChannelTFType
}

func (e monitoringNotificationChannelEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e monitoringNotificationChannelEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("monitoring_notification_channel: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e monitoringNotificationChannelEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Monitoring == nil {
		return nil, ErrEnrichClientUnavailable
	}
	fullName := monitoringNotificationChannelNameForEnrich(id, c.ProjectID)
	if fullName == "" {
		return nil, fmt.Errorf("monitoring_notification_channel: cannot derive resource name from Identity (Address=%q ImportID=%q NameHint=%q)",
			id.Address, id.ImportID, id.NameHint)
	}
	ch, err := e.fetch(ctx, c.Monitoring, fullName)
	if err != nil {
		if isMonitoringNotFound(err) {
			return nil, fmt.Errorf("monitoring_notification_channel: %s: %w", fullName, ErrNotFound)
		}
		return nil, fmt.Errorf("monitoring_notification_channel: get %s: %w", fullName, err)
	}
	typed := mapMonitoringNotificationChannel(ch, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("monitoring_notification_channel: marshal Attrs: %w", err)
	}
	return raw, nil
}

func monitoringNotificationChannelNameForEnrich(id *imported.ResourceIdentity, projectID string) string {
	if id.ImportID != "" {
		return id.ImportID
	}
	if id.NameHint != "" && projectID != "" {
		return fmt.Sprintf("projects/%s/notificationChannels/%s", projectID, id.NameHint)
	}
	return ""
}

func defaultMonitoringNotificationChannelFetch(ctx context.Context, svc *monitoringv3.Service, name string) (*monitoringv3.NotificationChannel, error) {
	return svc.Projects.NotificationChannels.Get(name).Context(ctx).Do()
}

func mapMonitoringNotificationChannel(ch *monitoringv3.NotificationChannel, projectID string) *generated.GoogleMonitoringNotificationChannel {
	out := &generated.GoogleMonitoringNotificationChannel{}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if ch == nil {
		return out
	}
	if ch.DisplayName != "" {
		out.DisplayName = generated.LiteralOf(ch.DisplayName)
	}
	if ch.Description != "" {
		out.Description = generated.LiteralOf(ch.Description)
	}
	if ch.Type != "" {
		out.Type_ = generated.LiteralOf(ch.Type)
	}
	if ch.Enabled {
		out.Enabled = generated.LiteralOf(true)
	}
	if len(ch.Labels) > 0 {
		// `labels` is the channel's functional configuration. Pass it
		// through unfiltered — these aren't system labels.
		labels := map[string]*generated.Value[string]{}
		for k, v := range ch.Labels {
			labels[k] = generated.LiteralOf(v)
		}
		out.Labels = labels
	}
	if len(ch.UserLabels) > 0 {
		ul := map[string]*generated.Value[string]{}
		for k, v := range ch.UserLabels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			ul[k] = generated.LiteralOf(v)
		}
		if len(ul) > 0 {
			out.UserLabels = ul
		}
	}
	return out
}
