package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// fakeStorageREST returns a fake JSON-API endpoint for the GCS storage
// client. The Go SDK's storage.NewClient honors option.WithEndpoint
// for the JSON API ("http://host/storage/v1/...").
func fakeStorageREST(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []option.ClientOption) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, []option.ClientOption{
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	}
}

// listBucketsResponse is the wire-shape GCS returns for buckets.list —
// items array with each bucket's metadata. Labels matter for the
// post-filter assertion.
const listBucketsResponse = `{
  "kind": "storage#buckets",
  "items": [
    {"name": "bkt-a", "location": "US", "storageClass": "STANDARD", "timeCreated": "2024-01-01T00:00:00Z", "labels": {"project": "io-foo"}},
    {"name": "bkt-b", "location": "EU", "storageClass": "NEARLINE", "timeCreated": "2024-01-02T00:00:00Z", "labels": {"project": "io-bar"}},
    {"name": "bkt-c", "location": "ASIA", "storageClass": "STANDARD", "timeCreated": "2024-01-03T00:00:00Z"}
  ]
}`

func TestInspectGCS_ListBuckets_AllReturned(t *testing.T) {
	t.Parallel()
	srv, opts := fakeStorageREST(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listBucketsResponse))
	})
	defer srv.Close()

	got, err := inspectGCS(context.Background(), "demo-proj", "list-buckets", "", opts...)
	require.NoError(t, err)

	buckets, ok := got.([]map[string]any)
	require.True(t, ok, "expected []map[string]any, got %T", got)
	// No project filter — all three buckets surface.
	assert.Len(t, buckets, 3)
	assert.Equal(t, "bkt-a", buckets[0]["name"])
	assert.Equal(t, "STANDARD", buckets[0]["storageClass"])
	assert.Equal(t, "EU", buckets[1]["location"])
}

func TestInspectGCS_ListBuckets_FiltersByProject(t *testing.T) {
	t.Parallel()
	srv, opts := fakeStorageREST(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listBucketsResponse))
	})
	defer srv.Close()

	got, err := inspectGCS(context.Background(), "demo-proj", "list-buckets", `{"project":"io-foo"}`, opts...)
	require.NoError(t, err)

	buckets, ok := got.([]map[string]any)
	require.True(t, ok)
	// Only bkt-a is labeled project=io-foo.
	require.Len(t, buckets, 1)
	assert.Equal(t, "bkt-a", buckets[0]["name"])
}

func TestInspectGCS_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectGCS(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported GCS action")
	assert.Contains(t, err.Error(), "list-buckets")
}

// TestInspectSecretManager_UnsupportedAction confirms the gRPC client
// constructor still allows the action switch to short-circuit before
// any RPC. SecretManager's gRPC happy path is exercised by the drift
// gate.
func TestInspectSecretManager_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectSecretManager(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Secret Manager action")
}

func TestInspectKMS_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectKMS(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud KMS action")
}

// TestInspectKMS_ListKeysMissingFilters guards the "must supply
// location and keyring" precondition — list-keys is the one KMS action
// that needs both filter values up front.
func TestInspectKMS_ListKeysMissingFilters(t *testing.T) {
	t.Parallel()
	_, err := inspectKMS(context.Background(), "demo-proj", "list-keys", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location and keyring")

	_, err = inspectKMS(context.Background(), "demo-proj", "list-keys", `{"location":"global"}`,
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location and keyring")
}
