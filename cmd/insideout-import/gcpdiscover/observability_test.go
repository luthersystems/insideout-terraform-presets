package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

// Single table-driven test covering all four observability types,
// since they share the same project-scoped pattern. Each row pins:
//   - the FromAsset trip (asset name → import ID)
//   - the discover-by-id pattern (full asset, import id, bare name,
//     and the empty-id error case)
func TestObservabilityDiscoverers_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		discoverer   Discoverer
		assetName    string
		wantTFType   string
		wantName     string
		wantImportID string
		// DiscoverByID-by-bare-name should resolve to the same name +
		// import ID (sanity-check the bare-name fallback).
	}{
		{
			name:         "monitoring_dashboard",
			discoverer:   newMonitoringDashboardDiscoverer(),
			assetName:    "//monitoring.googleapis.com/projects/real-proj/dashboards/io-foo-dash",
			wantTFType:   "google_monitoring_dashboard",
			wantName:     "io-foo-dash",
			wantImportID: "projects/real-proj/dashboards/io-foo-dash",
		},
		{
			name:         "monitoring_alert_policy",
			discoverer:   newMonitoringAlertPolicyDiscoverer(),
			assetName:    "//monitoring.googleapis.com/projects/real-proj/alertPolicies/io-foo-alert",
			wantTFType:   "google_monitoring_alert_policy",
			wantName:     "io-foo-alert",
			wantImportID: "projects/real-proj/alertPolicies/io-foo-alert",
		},
		{
			name:         "monitoring_notification_channel",
			discoverer:   newMonitoringNotificationChannelDiscoverer(),
			assetName:    "//monitoring.googleapis.com/projects/real-proj/notificationChannels/io-foo-chan",
			wantTFType:   "google_monitoring_notification_channel",
			wantName:     "io-foo-chan",
			wantImportID: "projects/real-proj/notificationChannels/io-foo-chan",
		},
		{
			name:         "logging_project_sink",
			discoverer:   newLoggingProjectSinkDiscoverer(),
			assetName:    "//logging.googleapis.com/projects/real-proj/sinks/io-foo-sink",
			wantTFType:   "google_logging_project_sink",
			wantName:     "io-foo-sink",
			wantImportID: "projects/real-proj/sinks/io-foo-sink",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.discoverer.FromAsset(addressBook{},
				gcpAssetResult{
					Name:      tc.assetName,
					AssetType: tc.discoverer.AssetType(),
					Project:   "real-proj",
				},
				"real-proj")
			if got.Identity.Type != tc.wantTFType {
				t.Errorf("Type=%q, want %q", got.Identity.Type, tc.wantTFType)
			}
			if got.Identity.NameHint != tc.wantName {
				t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, tc.wantName)
			}
			if got.Identity.ImportID != tc.wantImportID {
				t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, tc.wantImportID)
			}
			if got.Identity.Location != "" {
				t.Errorf("Location=%q, want empty (observability types are project-scoped, not regional)", got.Identity.Location)
			}

			// DiscoverByID coverage: asset name, import id, bare name,
			// and the empty-id error case.
			r, err := tc.discoverer.DiscoverByID(context.Background(), nil, tc.assetName, "real-proj")
			if err != nil {
				t.Fatalf("DiscoverByID(asset name) err=%v", err)
			}
			if r.Identity.NameHint != tc.wantName {
				t.Errorf("DiscoverByID(asset name): NameHint=%q, want %q", r.Identity.NameHint, tc.wantName)
			}
			r, err = tc.discoverer.DiscoverByID(context.Background(), nil, tc.wantImportID, "real-proj")
			if err != nil {
				t.Fatalf("DiscoverByID(import id) err=%v", err)
			}
			if r.Identity.NameHint != tc.wantName {
				t.Errorf("DiscoverByID(import id): NameHint=%q, want %q", r.Identity.NameHint, tc.wantName)
			}
			r, err = tc.discoverer.DiscoverByID(context.Background(), nil, tc.wantName, "real-proj")
			if err != nil {
				t.Fatalf("DiscoverByID(bare name) err=%v", err)
			}
			if r.Identity.NameHint != tc.wantName {
				t.Errorf("DiscoverByID(bare name): NameHint=%q, want %q", r.Identity.NameHint, tc.wantName)
			}
			_, err = tc.discoverer.DiscoverByID(context.Background(), nil, "", "real-proj")
			if !errors.Is(err, ErrNotSupported) {
				t.Errorf("DiscoverByID(\"\") err=%v, want ErrNotSupported", err)
			}
		})
	}
}
