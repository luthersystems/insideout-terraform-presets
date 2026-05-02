package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// Cloud Run, Cloud Functions, and Cloud Build all use gRPC clients that
// don't have a stable httptest fake pattern in this test setup — the
// happy paths are covered by the dispatcher drift gate
// (TestInspectCoversAllGCPServices). What we DO test here is the
// per-handler precondition + unsupported-action surface, which fires
// before any RPC and is the contract callers depend on for
// "describe-X requires Y in filters" errors.

func TestInspectCloudRun_DescribeService_MissingFilter(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudRun(context.Background(), "demo-proj", "describe-service", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location and service")

	_, err = inspectCloudRun(context.Background(), "demo-proj", "describe-service",
		`{"location":"us-central1"}`,
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location and service")
}

func TestInspectCloudRun_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudRun(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Run action")
}

func TestInspectCloudFunctions_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudFunctions(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Functions action")
}

func TestInspectCloudBuild_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudBuild(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Build action")
}

// TestCloudBuildMaxBuildsConstant locks the cap so a future change has
// to acknowledge it — the value drives the truncated-warning behavior
// and a quiet bump would silently change customer-visible report
// shapes.
func TestCloudBuildMaxBuildsConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 100, cloudBuildMaxBuilds,
		"changing the cap shifts list-builds payload size + truncated-log threshold")
}
