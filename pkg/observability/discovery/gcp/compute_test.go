package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// fakeComputeREST stands in for the Compute Engine REST endpoint. We
// route by URL path since that's stable across SDK versions; the
// handler answers two routes — aggregatedList for list-instances and
// the single-instance Get for describe-instance.
//
// REST clients accept option.WithEndpoint("http://...") + option.
// WithoutAuthentication, which is what makes this fake-server pattern
// work without standing up a TLS / OAuth stack.
func fakeComputeREST(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []option.ClientOption) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, []option.ClientOption{
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	}
}

// listInstancesAggregatedResponse is the wire-shape Compute returns for
// AggregatedList — a top-level `items` map keyed by zone selfLink.
const listInstancesAggregatedResponse = `{
  "id": "projects/demo-proj/aggregated/instances",
  "items": {
    "zones/us-central1-a": {
      "instances": [
        {"id": "1234", "name": "vm-a", "zone": "https://www.googleapis.com/compute/v1/projects/demo-proj/zones/us-central1-a"},
        {"id": "5678", "name": "vm-b", "zone": "https://www.googleapis.com/compute/v1/projects/demo-proj/zones/us-central1-a"}
      ]
    },
    "zones/us-central1-b": {
      "instances": [
        {"id": "9012", "name": "vm-c", "zone": "https://www.googleapis.com/compute/v1/projects/demo-proj/zones/us-central1-b"}
      ]
    }
  }
}`

func TestInspectCompute_ListInstances(t *testing.T) {
	t.Parallel()
	var captured *http.Request
	srv, opts := fakeComputeREST(t, func(w http.ResponseWriter, r *http.Request) {
		captured = r
		// AggregatedList is the only route we need to handle.
		if !strings.Contains(r.URL.Path, "/aggregated/instances") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listInstancesAggregatedResponse))
	})
	defer srv.Close()

	got, err := inspectCompute(context.Background(), "demo-proj", "list-instances", "", opts...)
	require.NoError(t, err)

	instances, ok := got.([]*computepb.Instance)
	require.True(t, ok, "expected []*computepb.Instance, got %T", got)
	assert.Len(t, instances, 3, "all three instances across both zones")

	// Project filter is enforced server-side (we don't simulate the
	// server-side filtering in the fake, but we do assert the request
	// reached the aggregated path with the project segment).
	require.NotNil(t, captured)
	assert.Contains(t, captured.URL.Path, "demo-proj")
}

func TestInspectCompute_ListInstances_AppliesProjectFilter(t *testing.T) {
	t.Parallel()
	var capturedQuery string
	srv, opts := fakeComputeREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":{}}`))
	})
	defer srv.Close()

	_, err := inspectCompute(context.Background(), "demo-proj", "list-instances", `{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	// Compute REST encodes the filter into the query string. We
	// assert it carries the GCE-legacy "labels.project=io-foo" shape
	// — this is the regression that #718 cared about.
	assert.Contains(t, capturedQuery, "labels.project")
	assert.Contains(t, capturedQuery, "io-foo")
}

func TestInspectCompute_DescribeInstance(t *testing.T) {
	t.Parallel()
	srv, opts := fakeComputeREST(t, func(w http.ResponseWriter, r *http.Request) {
		// The Get path looks like
		// /compute/v1/projects/<project>/zones/<zone>/instances/<name>
		if !strings.Contains(r.URL.Path, "/zones/us-central1-a/instances/vm-x") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"42","name":"vm-x","zone":"us-central1-a"}`))
	})
	defer srv.Close()

	got, err := inspectCompute(context.Background(), "demo-proj", "describe-instance",
		`{"zone":"us-central1-a","instance":"vm-x"}`, opts...)
	require.NoError(t, err)
	inst, ok := got.(*computepb.Instance)
	require.True(t, ok)
	assert.Equal(t, "vm-x", inst.GetName())
}

func TestInspectCompute_DescribeInstance_MissingFilters(t *testing.T) {
	t.Parallel()
	// No filter values means the handler must reject before any
	// network call — guard the contract.
	_, err := inspectCompute(context.Background(), "demo-proj", "describe-instance", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zone and instance")

	_, err = inspectCompute(context.Background(), "demo-proj", "describe-instance", `{"zone":"us-central1-a"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zone and instance")
}

func TestInspectCompute_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCompute(context.Background(), "demo-proj", "no-such", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Compute action")
	assert.Contains(t, err.Error(), "list-instances")
}

// TestInspectGKE_UnsupportedAction confirms the dispatcher's per-handler
// error path also kicks in for GKE — full happy-path requires gRPC
// fakery which we defer; the dispatcher drift gate exercises the
// happy-path routing.
func TestInspectGKE_UnsupportedAction(t *testing.T) {
	t.Parallel()
	// Use a fake endpoint so client construction succeeds; the action
	// switch fires before any RPC.
	_, err := inspectGKE(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported GKE action")
}

func TestInspectGKE_DescribeCluster_MissingFilters(t *testing.T) {
	t.Parallel()
	_, err := inspectGKE(context.Background(), "demo-proj", "describe-cluster", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location and cluster")
}

func TestInspectBastion_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectBastion(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Bastion action")
}

// TestInspectBastion_AppliesRoleAndProjectFilter exercises the bastion
// label filter — bastions are GCE instances tagged labels.role=bastion;
// we assert the AggregatedListInstances request carries BOTH labels
// when a project filter is also set. Mirrors the AND semantic in
// gcpLegacyLabelFilterAnd.
func TestInspectBastion_AppliesRoleAndProjectFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":{}}`))
	})
	defer srv.Close()

	_, err := inspectBastion(context.Background(), "demo-proj", "list-bastion-instances",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Contains(t, capturedFilter, "labels.role=bastion")
	assert.Contains(t, capturedFilter, "labels.project=io-foo")
	assert.Contains(t, capturedFilter, " AND ")
}

// TestInspectBastion_NoProjectStillSetsRoleFilter — when the caller
// hasn't supplied a project filter, bastion still scopes to
// labels.role=bastion alone.
func TestInspectBastion_NoProjectStillSetsRoleFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":{}}`))
	})
	defer srv.Close()

	_, err := inspectBastion(context.Background(), "demo-proj", "list-bastion-instances", "", opts...)
	require.NoError(t, err)
	assert.Equal(t, "labels.role=bastion", capturedFilter)
}
