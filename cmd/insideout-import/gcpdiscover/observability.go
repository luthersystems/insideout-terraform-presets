// Package-level: GCP observability discoverers (#377).
//
// Three monitoring resource types:
//
//   - google_monitoring_dashboard         (monitoring.googleapis.com/Dashboard)
//   - google_monitoring_alert_policy      (monitoring.googleapis.com/AlertPolicy)
//   - google_monitoring_notification_channel (monitoring.googleapis.com/NotificationChannel)
//
// All three are label-less per the provider schema → ScopeStyleNamePrefix.
// They are project-scoped (no /locations/ segment) and the trailing
// short name is the import-ID segment.
//
// Kept in one file because the per-type code is ~40 LOC of boilerplate
// each; per-type files would buy zero separation and 3x the directory
// noise.
//
// google_logging_project_sink was originally part of this slice but
// removed before merge: Cloud Asset's SearchAllResources rejects
// `logging.googleapis.com/Sink` as an unsupported asset type (see
// https://cloud.google.com/asset-inventory/docs/supported-asset-types
// — the logging family covers LogBucket / LogMetric / Settings but
// not Sink). A non-CAI discoverer is a follow-up.
package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	monitoringAssetHost = "monitoring.googleapis.com"

	monitoringDashboardTFType           = "google_monitoring_dashboard"
	monitoringDashboardAssetType        = "monitoring.googleapis.com/Dashboard"
	monitoringAlertPolicyTFType         = "google_monitoring_alert_policy"
	monitoringAlertPolicyAssetType      = "monitoring.googleapis.com/AlertPolicy"
	monitoringNotificationChannelTFType = "google_monitoring_notification_channel"
	monitoringNotificationChannelAsset  = "monitoring.googleapis.com/NotificationChannel"
)

// --- google_monitoring_dashboard ---

type monitoringDashboardDiscoverer struct{}

func newMonitoringDashboardDiscoverer() Discoverer { return &monitoringDashboardDiscoverer{} }

func (monitoringDashboardDiscoverer) ResourceType() string   { return monitoringDashboardTFType }
func (monitoringDashboardDiscoverer) AssetType() string      { return monitoringDashboardAssetType }
func (monitoringDashboardDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (monitoringDashboardDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/dashboards/%s", projectID, name)
	return makeImportedResource(book, monitoringDashboardTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (monitoringDashboardDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := projectScopedNameFromID(id, "/dashboards/", "monitoring_dashboard")
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/dashboards/%s", projectID, name)
	return makeImportedResource(addressBook{}, monitoringDashboardTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/dashboards/%s", monitoringAssetHost, projectID, name),
	}, nil), nil
}

// --- google_monitoring_alert_policy ---

type monitoringAlertPolicyDiscoverer struct{}

func newMonitoringAlertPolicyDiscoverer() Discoverer { return &monitoringAlertPolicyDiscoverer{} }

func (monitoringAlertPolicyDiscoverer) ResourceType() string   { return monitoringAlertPolicyTFType }
func (monitoringAlertPolicyDiscoverer) AssetType() string      { return monitoringAlertPolicyAssetType }
func (monitoringAlertPolicyDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (monitoringAlertPolicyDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/alertPolicies/%s", projectID, name)
	return makeImportedResource(book, monitoringAlertPolicyTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (monitoringAlertPolicyDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := projectScopedNameFromID(id, "/alertPolicies/", "monitoring_alert_policy")
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/alertPolicies/%s", projectID, name)
	return makeImportedResource(addressBook{}, monitoringAlertPolicyTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/alertPolicies/%s", monitoringAssetHost, projectID, name),
	}, nil), nil
}

// --- google_monitoring_notification_channel ---

type monitoringNotificationChannelDiscoverer struct{}

func newMonitoringNotificationChannelDiscoverer() Discoverer {
	return &monitoringNotificationChannelDiscoverer{}
}

func (monitoringNotificationChannelDiscoverer) ResourceType() string {
	return monitoringNotificationChannelTFType
}
func (monitoringNotificationChannelDiscoverer) AssetType() string {
	return monitoringNotificationChannelAsset
}
func (monitoringNotificationChannelDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (monitoringNotificationChannelDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/notificationChannels/%s", projectID, name)
	return makeImportedResource(book, monitoringNotificationChannelTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (monitoringNotificationChannelDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := projectScopedNameFromID(id, "/notificationChannels/", "monitoring_notification_channel")
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/notificationChannels/%s", projectID, name)
	return makeImportedResource(addressBook{}, monitoringNotificationChannelTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/notificationChannels/%s", monitoringAssetHost, projectID, name),
	}, nil), nil
}

// projectScopedNameFromID pulls the trailing short name from a
// project-scoped (no /locations/<l>/) Cloud Asset or import shape.
// Shared by the three observability discoverers — they only differ by
// the collection marker. Bare names are accepted as a fallback.
func projectScopedNameFromID(id, marker, typeLabel string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%s: empty id: %w", typeLabel, ErrNotSupported)
	}
	if idx := strings.Index(id, marker); idx >= 0 {
		rest := id[idx+len(marker):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("%s: unrecognized id %q: %w", typeLabel, id, ErrNotSupported)
	}
	return id, nil
}
