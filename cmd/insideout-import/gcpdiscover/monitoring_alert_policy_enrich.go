package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
	monitoringv3 "google.golang.org/api/monitoring/v3"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// monitoringAlertPolicyEnricher implements AttributeEnricher AND
// ByIDEnricher for google_monitoring_alert_policy. Pairs with
// monitoringAlertPolicyDiscoverer.
//
// AlertPolicies.Get takes a single fully-qualified name of the form
// `projects/<p>/alertPolicies/<id>`. The discoverer puts that exact
// string in ImportID and the short id in NameHint, so the enricher
// uses ImportID directly (falling back to constructing it).
//
// Mapping covers the top-level attributes (display_name, combiner,
// enabled, notification_channels, user_labels, severity) and the
// commonly-used nested blocks (documentation, alert_strategy). The
// deeply-nested conditions block is mapped at the display-name level
// only — the full condition tree (threshold, absent, prometheus_query)
// has many sub-fields whose hand-roll cost is high relative to the
// value-add vs. a follow-up enrichgen pass. Partial coverage > none.
//
// Computed-only fields skipped per decision #5: id, name, creation_record.
type monitoringAlertPolicyEnricher struct {
	fetch func(ctx context.Context, svc *monitoringv3.Service, name string) (*monitoringv3.AlertPolicy, error)
}

func newMonitoringAlertPolicyEnricher() AttributeEnricher {
	return &monitoringAlertPolicyEnricher{fetch: defaultMonitoringAlertPolicyFetch}
}

var (
	_ AttributeEnricher = (*monitoringAlertPolicyEnricher)(nil)
	_ ByIDEnricher      = (*monitoringAlertPolicyEnricher)(nil)
)

func (monitoringAlertPolicyEnricher) ResourceType() string {
	return monitoringAlertPolicyTFType
}

func (e monitoringAlertPolicyEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e monitoringAlertPolicyEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("monitoring_alert_policy: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e monitoringAlertPolicyEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Monitoring == nil {
		return nil, ErrEnrichClientUnavailable
	}
	fullName := monitoringAlertPolicyNameForEnrich(id, c.ProjectID)
	if fullName == "" {
		return nil, fmt.Errorf("monitoring_alert_policy: cannot derive resource name from Identity (Address=%q ImportID=%q NameHint=%q)",
			id.Address, id.ImportID, id.NameHint)
	}
	p, err := e.fetch(ctx, c.Monitoring, fullName)
	if err != nil {
		if isMonitoringNotFound(err) {
			return nil, fmt.Errorf("monitoring_alert_policy: %s: %w", fullName, ErrNotFound)
		}
		return nil, fmt.Errorf("monitoring_alert_policy: get %s: %w", fullName, err)
	}
	typed := mapMonitoringAlertPolicy(p, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("monitoring_alert_policy: marshal Attrs: %w", err)
	}
	return raw, nil
}

// monitoringAlertPolicyNameForEnrich returns the fully-qualified
// `projects/<p>/alertPolicies/<id>` resource name from the Identity.
// Precedence: ImportID (canonical), then NameHint + projectID.
func monitoringAlertPolicyNameForEnrich(id *imported.ResourceIdentity, projectID string) string {
	if id.ImportID != "" {
		return id.ImportID
	}
	if id.NameHint != "" && projectID != "" {
		return fmt.Sprintf("projects/%s/alertPolicies/%s", projectID, id.NameHint)
	}
	return ""
}

func defaultMonitoringAlertPolicyFetch(ctx context.Context, svc *monitoringv3.Service, name string) (*monitoringv3.AlertPolicy, error) {
	return svc.Projects.AlertPolicies.Get(name).Context(ctx).Do()
}

func isMonitoringNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

func mapMonitoringAlertPolicy(p *monitoringv3.AlertPolicy, projectID string) *generated.GoogleMonitoringAlertPolicy {
	out := &generated.GoogleMonitoringAlertPolicy{}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if p == nil {
		return out
	}
	if p.DisplayName != "" {
		out.DisplayName = generated.LiteralOf(p.DisplayName)
	}
	if p.Combiner != "" {
		out.Combiner = generated.LiteralOf(p.Combiner)
	}
	if p.Enabled {
		out.Enabled = generated.LiteralOf(true)
	}
	if p.Severity != "" {
		out.Severity = generated.LiteralOf(p.Severity)
	}
	if len(p.NotificationChannels) > 0 {
		chs := make([]*generated.Value[string], 0, len(p.NotificationChannels))
		for _, c := range p.NotificationChannels {
			chs = append(chs, generated.LiteralOf(c))
		}
		out.NotificationChannels = chs
	}
	if len(p.UserLabels) > 0 {
		labels := map[string]*generated.Value[string]{}
		for k, v := range p.UserLabels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			labels[k] = generated.LiteralOf(v)
		}
		if len(labels) > 0 {
			out.UserLabels = labels
		}
	}
	if p.Documentation != nil {
		doc := generated.GoogleMonitoringAlertPolicyDocumentation{}
		populated := false
		if p.Documentation.Content != "" {
			doc.Content = generated.LiteralOf(p.Documentation.Content)
			populated = true
		}
		if p.Documentation.MimeType != "" {
			doc.MimeType = generated.LiteralOf(p.Documentation.MimeType)
			populated = true
		}
		if p.Documentation.Subject != "" {
			doc.Subject = generated.LiteralOf(p.Documentation.Subject)
			populated = true
		}
		if len(p.Documentation.Links) > 0 {
			links := make([]generated.GoogleMonitoringAlertPolicyDocumentationLinks, 0, len(p.Documentation.Links))
			for _, l := range p.Documentation.Links {
				if l == nil {
					continue
				}
				link := generated.GoogleMonitoringAlertPolicyDocumentationLinks{}
				if l.DisplayName != "" {
					link.DisplayName = generated.LiteralOf(l.DisplayName)
				}
				if l.Url != "" {
					link.URL = generated.LiteralOf(l.Url)
				}
				links = append(links, link)
			}
			if len(links) > 0 {
				doc.Links = links
			}
			populated = true
		}
		if populated {
			out.Documentation = []generated.GoogleMonitoringAlertPolicyDocumentation{doc}
		}
	}
	if p.AlertStrategy != nil {
		strat := generated.GoogleMonitoringAlertPolicyAlertStrategy{}
		populated := false
		if p.AlertStrategy.AutoClose != "" {
			strat.AutoClose = generated.LiteralOf(p.AlertStrategy.AutoClose)
			populated = true
		}
		if len(p.AlertStrategy.NotificationPrompts) > 0 {
			prompts := make([]*generated.Value[string], 0, len(p.AlertStrategy.NotificationPrompts))
			for _, np := range p.AlertStrategy.NotificationPrompts {
				prompts = append(prompts, generated.LiteralOf(np))
			}
			strat.NotificationPrompts = prompts
			populated = true
		}
		if p.AlertStrategy.NotificationRateLimit != nil && p.AlertStrategy.NotificationRateLimit.Period != "" {
			strat.NotificationRateLimit = []generated.GoogleMonitoringAlertPolicyAlertStrategyNotificationRateLimit{
				{Period: generated.LiteralOf(p.AlertStrategy.NotificationRateLimit.Period)},
			}
			populated = true
		}
		if populated {
			out.AlertStrategy = []generated.GoogleMonitoringAlertPolicyAlertStrategy{strat}
		}
	}
	if len(p.Conditions) > 0 {
		conds := make([]generated.GoogleMonitoringAlertPolicyConditions, 0, len(p.Conditions))
		for _, c := range p.Conditions {
			if c == nil {
				continue
			}
			cond := generated.GoogleMonitoringAlertPolicyConditions{}
			if c.DisplayName != "" {
				cond.DisplayName = generated.LiteralOf(c.DisplayName)
			}
			conds = append(conds, cond)
		}
		if len(conds) > 0 {
			out.Conditions = conds
		}
	}
	return out
}
