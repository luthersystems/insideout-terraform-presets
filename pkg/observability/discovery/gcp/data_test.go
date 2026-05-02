package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// fakeSQLAdminREST stands in for the Cloud SQL Admin REST endpoint.
// sqladmin.NewService is a googleapi REST client and respects
// option.WithEndpoint.
func fakeSQLAdminREST(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []option.ClientOption) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, []option.ClientOption{
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	}
}

const listSQLInstancesResponse = `{
  "kind": "sql#instancesList",
  "items": [
    {"name":"db-a","databaseVersion":"POSTGRES_15","settings":{"userLabels":{"project":"io-foo"}}},
    {"name":"db-b","databaseVersion":"MYSQL_8_0","settings":{"userLabels":{"project":"io-bar"}}},
    {"name":"db-c","databaseVersion":"POSTGRES_15"}
  ]
}`

func TestInspectCloudSQL_ListInstances_AllReturned(t *testing.T) {
	t.Parallel()
	srv, opts := fakeSQLAdminREST(t, func(w http.ResponseWriter, r *http.Request) {
		// sqladmin paths look like /sql/v1beta4/projects/<project>/instances
		// (or v1 — both shapes resolve to the same backend). The fake
		// just answers anything that asks for /instances.
		if !strings.Contains(r.URL.Path, "/instances") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listSQLInstancesResponse))
	})
	defer srv.Close()

	got, err := inspectCloudSQL(context.Background(), "demo-proj", "list-instances", "", opts...)
	require.NoError(t, err)

	items, ok := got.([]*sqladmin.DatabaseInstance)
	require.True(t, ok, "expected []*sqladmin.DatabaseInstance, got %T", got)
	assert.Len(t, items, 3)
}

func TestInspectCloudSQL_ListInstances_FiltersByProject(t *testing.T) {
	t.Parallel()
	srv, opts := fakeSQLAdminREST(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listSQLInstancesResponse))
	})
	defer srv.Close()

	got, err := inspectCloudSQL(context.Background(), "demo-proj", "list-instances",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)

	items, ok := got.([]*sqladmin.DatabaseInstance)
	require.True(t, ok)
	require.Len(t, items, 1, "only db-a is labeled project=io-foo")
	assert.Equal(t, "db-a", items[0].Name)
}

func TestInspectCloudSQL_DescribeInstance(t *testing.T) {
	t.Parallel()
	srv, opts := fakeSQLAdminREST(t, func(w http.ResponseWriter, r *http.Request) {
		// Get path: /sql/v1/projects/<project>/instances/<name>
		if !strings.Contains(r.URL.Path, "/instances/db-x") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"db-x","databaseVersion":"POSTGRES_15"}`))
	})
	defer srv.Close()

	got, err := inspectCloudSQL(context.Background(), "demo-proj", "describe-instance",
		`{"instance":"db-x"}`, opts...)
	require.NoError(t, err)
	inst, ok := got.(*sqladmin.DatabaseInstance)
	require.True(t, ok)
	assert.Equal(t, "db-x", inst.Name)
}

func TestInspectCloudSQL_DescribeInstance_MissingFilter(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudSQL(context.Background(), "demo-proj", "describe-instance", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance in filters")
}

func TestInspectCloudSQL_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudSQL(context.Background(), "demo-proj", "no-such", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud SQL action")
}

// --- Memorystore + Firestore: precondition + unsupported-action only.
// Their happy paths are gRPC; the drift gate covers dispatch routing.

func TestInspectMemorystore_DescribeInstance_MissingFilter(t *testing.T) {
	t.Parallel()
	_, err := inspectMemorystore(context.Background(), "demo-proj", "describe-instance", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location and instance")
}

func TestInspectMemorystore_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectMemorystore(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Memorystore action")
}

func TestInspectFirestore_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectFirestore(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Firestore action")
}
