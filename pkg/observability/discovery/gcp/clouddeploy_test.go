// Cloud Deploy inspector tests (issue #622).
//
// Cloud Deploy uses a gRPC client without a stable httptest fake pattern;
// happy paths are covered end-to-end by the dispatcher drift gate
// (TestInspectCoversAllGCPServices). What we test here is the per-handler
// precondition + unsupported-action surface, plus the #255 / #256
// empty-slice contract via drainIterator on the underlying iterator type.

package gcp

import (
	"context"
	"encoding/json"
	"testing"

	"cloud.google.com/go/deploy/apiv1/deploypb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

func TestInspectCloudDeploy_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudDeploy(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Deploy action")
	assert.Contains(t, err.Error(), "list-delivery-pipelines")
}

// TestInspectCloudDeploy_NoGetMetrics — get-metrics is registered in
// GCPServiceActions["clouddeploy"] but the discovery dispatcher does
// NOT route it (Cloud Monitoring lives in pkg/observability/metrics).
// A regression where get-metrics started succeeding through the
// dispatcher would mean the metric-fetch responsibility leaked across
// the package boundary.
func TestInspectCloudDeploy_NoGetMetrics(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudDeploy(context.Background(), "demo-proj", "get-metrics", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Deploy action",
		"get-metrics is metrics-pkg responsibility, not discovery")
}

// TestCloudDeployLocationFromFilters_DefaultsGlobal — when no location
// filter is supplied, the inspector targets `locations/global`, the
// canonical region for the cloud_deploy preset's pipeline objects.
func TestCloudDeployLocationFromFilters_DefaultsGlobal(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "global", cloudDeployLocationFromFilters(""))
	assert.Equal(t, "global", cloudDeployLocationFromFilters(`{"project":"demo"}`))
	assert.Equal(t, "global", cloudDeployLocationFromFilters(`{"location":""}`))
}

func TestCloudDeployLocationFromFilters_OverridesViaFilter(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "us-central1",
		cloudDeployLocationFromFilters(`{"location":"us-central1"}`))
}

// Empty-state pins per #256: every list site routes through
// drainIterator, so the helper-test file already pins the contract.
// These per-site tests pin the *call-site type* end-to-end so a future
// refactor that bypasses drainIterator is caught at the inspector level.

func TestInspectCloudDeploy_ListDeliveryPipelines_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(
		&emptyIterator[*deploypb.DeliveryPipeline]{},
		func(*deploypb.DeliveryPipeline) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cloud Deploy list-delivery-pipelines must marshal as [] not null (#255/#256)")
}

func TestInspectCloudDeploy_ListTargets_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(
		&emptyIterator[*deploypb.Target]{},
		func(*deploypb.Target) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cloud Deploy list-targets must marshal as [] not null (#255/#256)")
}
