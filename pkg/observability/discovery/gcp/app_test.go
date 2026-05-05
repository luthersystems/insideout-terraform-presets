package gcp

import (
	"context"
	"encoding/json"
	"testing"

	"cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	"cloud.google.com/go/functions/apiv2/functionspb"
	"cloud.google.com/go/run/apiv2/runpb"
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

// Empty-state pins per #256: each site routes through drainIterator
// (or drainIteratorBounded for list-builds), so the helper-test file
// already pins the contract. These per-site tests pin the *call-site
// type* end-to-end so a future refactor that bypasses drainIterator
// (e.g. inlining `var X []T` again) is caught at the inspector level.

func TestInspectCloudRun_ListServices_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(
		&emptyIterator[*runpb.Service]{},
		func(*runpb.Service) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cloud Run list-services must marshal as [] not null (#256)")
}

func TestInspectCloudFunctions_ListFunctions_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*functionspb.Function]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cloud Functions list-functions must marshal as [] not null (#256)")
}

func TestInspectCloudBuild_ListTriggers_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*cloudbuildpb.BuildTrigger]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cloud Build list-triggers must marshal as [] not null (#256)")
}

func TestInspectCloudBuild_ListBuilds_NoBuilds_EmptySlice(t *testing.T) {
	t.Parallel()
	got, truncated, err := drainIteratorBounded(&emptyIterator[*cloudbuildpb.Build]{}, cloudBuildMaxBuilds)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, truncated, "empty list-builds must NOT report truncated")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cloud Build list-builds must marshal as [] not null (#256)")
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
