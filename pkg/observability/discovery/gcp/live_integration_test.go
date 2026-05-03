//go:build integration

// Live GCP smoke tests. Run with:
//
//	go test -tags=integration ./pkg/observability/discovery/gcp/... -v -run TestLive
//
// Requires:
//   - Application Default Credentials in the environment (e.g.
//     `gcloud auth application-default login`, or a service-account
//     key path in GOOGLE_APPLICATION_CREDENTIALS).
//   - LIVE_GCP_PROJECT_ID set to the Compute project to probe (must
//     have the Compute API enabled — even if no CDN-enabled backend
//     services are present).
//
// Build-tag-gated so CI doesn't exercise these. Used to confirm
// inspector helpers compose cleanly against the real GCP REST surface.
//
// The Cloud CDN test pins #239: backendServices.aggregatedList rejects
// the GCE legacy filter dialect (`labels.project=<value>`) with HTTP
// 400 "Invalid list filter expression". The fix flipped the inspector
// to AIP-160 (`labels.project = "<value>"`); this live probe confirms
// the call actually returns 200 OK against a real project.

package gcp

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveProjectOrSkip returns the project ID from LIVE_GCP_PROJECT_ID or
// skips the test. ADC is consumed by the SDK clients implicitly.
func liveProjectOrSkip(t *testing.T) string {
	t.Helper()
	projectID := os.Getenv("LIVE_GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("LIVE_GCP_PROJECT_ID not set; export the project ID to run live GCP probes")
	}
	return projectID
}

// TestLive_InspectCloudCDN_NoCDNFilter exercises the AggregatedList
// path with no `project` label filter. Should return cleanly (empty
// slice or list of CDN-enabled backend services) with no HTTP 400.
//
// This is the regression test for #239: the bug surfaced as a 400
// "Invalid list filter expression" when the legacy filter dialect was
// passed to backendServices.aggregatedList. With no filter on the
// request at all, the call must succeed.
func TestLive_InspectCloudCDN_NoCDNFilter(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)

	got, err := inspectCloudCDN(context.Background(), projectID, "list-backend-services-cdn", "")
	require.NoError(t, err, "inspectCloudCDN with no project filter must not error against a real project")
	t.Logf("inspectCloudCDN returned %T with content %v", got, got)
}

// TestLive_InspectCloudCDN_WithProjectFilter exercises the
// AggregatedList path with the AIP-160 `labels.project = "<value>"`
// filter. The label value is provided via LIVE_GCP_PROJECT_LABEL — set
// it to a known label value on the account, or to the bare project ID
// (matches every backend service that carries `project = <project_id>`
// in its labels). Skips if not set.
//
// Pre-#239 fix this returned HTTP 400 immediately; post-fix it must
// return 200 OK with an empty (or populated) slice.
func TestLive_InspectCloudCDN_WithProjectFilter(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)

	label := os.Getenv("LIVE_GCP_PROJECT_LABEL")
	if label == "" {
		t.Skip("LIVE_GCP_PROJECT_LABEL not set; export a project-label value to test the filter path")
	}

	filters := `{"project":"` + label + `"}`
	got, err := inspectCloudCDN(context.Background(), projectID, "list-backend-services-cdn", filters)
	require.NoError(t, err,
		"inspectCloudCDN with project-label filter must use AIP-160 dialect — HTTP 400 here means the filter dialect regressed (#239)")
	t.Logf("inspectCloudCDN(label=%q) returned %T with content %v", label, got, got)
}

// TestLive_CloudCDNAggregatedListRequest_FilterShape is a defense-in-
// depth pin: the request constructor's output matches AIP-160 even at
// runtime under live build tags (a unit-test pin already exists; this
// catches the case where someone disables the unit test or skips the
// non-integration build).
func TestLive_CloudCDNAggregatedListRequest_FilterShape(t *testing.T) {
	t.Parallel()
	req := cloudCDNAggregatedListRequest("p", `{"project":"io-qtyb4nkwp5n8"}`)
	require.NotNil(t, req.Filter)
	assert.Equal(t, `labels.project = "io-qtyb4nkwp5n8"`, *req.Filter)
}
