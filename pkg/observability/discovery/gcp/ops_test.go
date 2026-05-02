package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// Cloud Logging, Cloud Monitoring, and Pub/Sub all use gRPC clients.
// As with app_test.go, the dispatcher drift gate covers happy-path
// routing; this file pins down precondition + unsupported-action
// behavior — the deterministic surface that doesn't need an RPC.

func TestInspectLogging_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectLogging(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Logging action")
}

func TestInspectCloudMonitoring_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudMonitoring(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Monitoring action")
}

// TestInspectCloudMonitoring_NoGetMetrics asserts that get-metrics is
// NOT routed by the discovery layer (per dispatcher.go's stated
// contract: metrics live in pkg/observability/metrics). A regression
// here would mean the dispatcher accidentally took on the
// metric-fetch responsibility.
func TestInspectCloudMonitoring_NoGetMetrics(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudMonitoring(context.Background(), "demo-proj", "get-metrics", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Monitoring action",
		"get-metrics is metrics-pkg responsibility, not discovery")
}

func TestInspectPubSub_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectPubSub(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Pub/Sub action")
}

// TestIdentityPlatformMaxTenantsConstant locks the cap. Same rationale
// as cloudBuildMaxBuilds: a quiet bump shifts customer-visible report
// shapes and the truncated-warning trigger.
func TestIdentityPlatformMaxTenantsConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1000, identityPlatformMaxTenants)
}
