package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/googleapi"
	monitoringv1 "google.golang.org/api/monitoring/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// monitoringDashboardEnricher implements AttributeEnricher AND
// ByIDEnricher for google_monitoring_dashboard. Pairs with
// monitoringDashboardDiscoverer.
//
// Dashboards live in google.golang.org/api/monitoring/v1 — separate
// from monitoring/v3 (where AlertPolicies / NotificationChannels live).
// Projects.Dashboards.Get takes a fully-qualified name of the form
// `projects/<p>/dashboards/<id>`. The discoverer puts that string in
// ImportID and the short id in NameHint.
//
// Mapping rationale: the TF schema is unusual — it stores the entire
// dashboard layout as a single `dashboard_json` string attribute
// (everything except project / labels is opaque to TF). So the enricher
// marshals the SDK Dashboard struct back to JSON (minus the fields TF
// derives from its outer schema — project / etag / name) and writes
// that string into dashboard_json. This matches what the provider
// itself does on read.
//
// Computed-only TF fields skipped per decision #5: id.
type monitoringDashboardEnricher struct {
	fetch func(ctx context.Context, svc *monitoringv1.Service, name string) (*monitoringv1.Dashboard, error)
}

func newMonitoringDashboardEnricher() AttributeEnricher {
	return &monitoringDashboardEnricher{fetch: defaultMonitoringDashboardFetch}
}

var (
	_ AttributeEnricher = (*monitoringDashboardEnricher)(nil)
	_ ByIDEnricher      = (*monitoringDashboardEnricher)(nil)
)

func (monitoringDashboardEnricher) ResourceType() string {
	return monitoringDashboardTFType
}

func (e monitoringDashboardEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e monitoringDashboardEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("monitoring_dashboard: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e monitoringDashboardEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.MonitoringDashboard == nil {
		return nil, ErrEnrichClientUnavailable
	}
	fullName := monitoringDashboardNameForEnrich(id, c.ProjectID)
	if fullName == "" {
		return nil, fmt.Errorf("monitoring_dashboard: cannot derive resource name from Identity (Address=%q ImportID=%q NameHint=%q)",
			id.Address, id.ImportID, id.NameHint)
	}
	d, err := e.fetch(ctx, c.MonitoringDashboard, fullName)
	if err != nil {
		if isMonitoringDashboardNotFound(err) {
			return nil, fmt.Errorf("monitoring_dashboard: %s: %w", fullName, ErrNotFound)
		}
		return nil, fmt.Errorf("monitoring_dashboard: get %s: %w", fullName, err)
	}
	typed, err := mapMonitoringDashboard(d, c.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("monitoring_dashboard: build typed Attrs: %w", err)
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("monitoring_dashboard: marshal Attrs: %w", err)
	}
	return raw, nil
}

// monitoringDashboardNameForEnrich returns the fully-qualified
// `projects/<p>/dashboards/<id>` resource name. Precedence: ImportID,
// then NameHint + projectID.
func monitoringDashboardNameForEnrich(id *imported.ResourceIdentity, projectID string) string {
	if id.ImportID != "" {
		return id.ImportID
	}
	if id.NameHint != "" && projectID != "" {
		return fmt.Sprintf("projects/%s/dashboards/%s", projectID, id.NameHint)
	}
	return ""
}

func defaultMonitoringDashboardFetch(ctx context.Context, svc *monitoringv1.Service, name string) (*monitoringv1.Dashboard, error) {
	return svc.Projects.Dashboards.Get(name).Context(ctx).Do()
}

func isMonitoringDashboardNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapMonitoringDashboard converts a *monitoringv1.Dashboard into the
// typed Layer-1 *generated.GoogleMonitoringDashboard. The dashboard
// content is serialized to JSON and stored in the dashboard_json
// attribute. Outer-schema fields (Name, Etag) are zeroed before
// marshalling so the JSON shape matches what the provider's own read
// produces.
func mapMonitoringDashboard(d *monitoringv1.Dashboard, projectID string) (*generated.GoogleMonitoringDashboard, error) {
	out := &generated.GoogleMonitoringDashboard{}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if d == nil {
		return out, nil
	}
	// Clone the dashboard so we can strip TF-outer-schema fields
	// without mutating the caller's copy.
	clone := *d
	clone.Name = ""
	clone.Etag = ""
	clone.ServerResponse = googleapi.ServerResponse{}
	payload, err := json.Marshal(&clone)
	if err != nil {
		return nil, fmt.Errorf("marshal dashboard payload: %w", err)
	}
	if len(payload) > 0 && string(payload) != "{}" {
		out.DashboardJSON = generated.LiteralOf(string(payload))
	}
	return out, nil
}
