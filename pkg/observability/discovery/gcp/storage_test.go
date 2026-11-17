package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"cloud.google.com/go/storage"
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
    {"name": "bkt-a", "location": "US", "storageClass": "STANDARD", "timeCreated": "2024-01-01T00:00:00Z", "versioning": {"enabled": true}, "labels": {"project": "io-foo"},
     "lifecycle": {"rule": [{"action": {"type": "SetStorageClass", "storageClass": "NEARLINE"}, "condition": {"age": 30, "matchesStorageClass": ["STANDARD"]}}, {"action": {"type": "Delete"}, "condition": {"age": 365}}]}},
    {"name": "bkt-b", "location": "EU", "storageClass": "NEARLINE", "timeCreated": "2024-01-02T00:00:00Z", "labels": {"project": "io-bar"}},
    {"name": "bkt-c", "location": "ASIA", "storageClass": "STANDARD", "timeCreated": "2024-01-03T00:00:00Z"},
    {"name": "bkt-d", "location": "US", "storageClass": "STANDARD", "timeCreated": "2024-01-04T00:00:00Z", "versioning": {"enabled": false}, "labels": {"project": "io-baz"}}
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
	// No project filter — all four buckets surface.
	require.Len(t, buckets, 4)
	assert.Equal(t, "bkt-a", buckets[0]["name"])
	assert.Equal(t, "STANDARD", buckets[0]["storageClass"])
	assert.Equal(t, "EU", buckets[1]["location"])
	// versioning comes straight off BucketAttrs.VersioningEnabled (#712):
	// bkt-a has versioning enabled, bkt-b/bkt-c default to false.
	assert.Equal(t, true, buckets[0]["versioning"])
	assert.Equal(t, false, buckets[1]["versioning"])
	// bkt-d carries an EXPLICIT "versioning":{"enabled":false} block on the
	// wire (not a missing block defaulting to Go's zero value), proving the
	// false path is extracted from real REST data rather than an
	// encoding/json default. #712.
	assert.Equal(t, "bkt-d", buckets[3]["name"])
	assert.Equal(t, false, buckets[3]["versioning"])

	// lifecycle is carried through in the JSON API's shape (#928) so
	// extractGCPGCSConfig can report lifecyclePolicy. A bucket with no
	// rules still gets lifecycle.rule = [], never null.
	raw, err := json.Marshal(buckets[0]["lifecycle"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"rule":[
		{"action":{"type":"SetStorageClass","storageClass":"NEARLINE"},"condition":{"age":30,"matchesStorageClass":["STANDARD"]}},
		{"action":{"type":"Delete"},"condition":{"age":365}}]}`, string(raw))
	raw, err = json.Marshal(buckets[1]["lifecycle"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"rule":[]}`, string(raw))
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

// Empty-state pins per #256 for the storage-plane sites.

func TestInspectGCS_ListBuckets_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	// inspectGCS routes through drainIterator + bucketAttrsToMaps. The
	// pin covers the post-transform shape: when drainIterator returns
	// []*storage.BucketAttrs{}, bucketAttrsToMaps returns
	// []map[string]any{} (NOT nil) so the JSON wire is `[]`.
	got := bucketAttrsToMaps(nil)
	require.NotNil(t, got, "bucketAttrsToMaps(nil) must be non-nil for empty-state contract")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty GCS list-buckets must marshal as [] not null (#256)")
}

func TestInspectGCS_DrainEmpty_EmptySlice(t *testing.T) {
	t.Parallel()
	// Also pin the upstream drainIterator path (the iterator yields no
	// BucketAttrs at all).
	got, err := drainIterator(
		&emptyIterator[*storage.BucketAttrs]{},
		func(*storage.BucketAttrs) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	maps := bucketAttrsToMaps(got)
	b, err := json.Marshal(maps)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty GCS drain → bucketAttrsToMaps must marshal as [] not null (#256)")
}

func TestInspectSecretManager_ListSecrets_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(
		&emptyIterator[*secretmanagerpb.Secret]{},
		func(*secretmanagerpb.Secret) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Secret Manager list-secrets must marshal as [] not null (#256)")
}

func TestInspectKMS_ListKeyrings_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*kmspb.KeyRing]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty KMS list-keyrings must marshal as [] not null (#256)")
}

func TestInspectKMS_ListKeys_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(
		&emptyIterator[*kmspb.CryptoKey]{},
		func(*kmspb.CryptoKey) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty KMS list-keys must marshal as [] not null (#256)")
}

// TestKeyRingsWithKeyCounts pins the list-keyrings numKeys enrichment
// (#928): each ring gets its ListCryptoKeys count, a failed count leaves
// numKeys out without failing the call, and only the first
// kmsKeyCountFanOutCap rings are counted.
func TestKeyRingsWithKeyCounts(t *testing.T) {
	t.Parallel()

	t.Run("CountsAndFailure", func(t *testing.T) {
		t.Parallel()
		rings := []*kmspb.KeyRing{
			{Name: "projects/p/locations/global/keyRings/a"},
			{Name: "projects/p/locations/global/keyRings/b"},
		}
		var asked []string
		got := keyRingsWithKeyCounts(context.Background(), rings, func(_ context.Context, parent string) (int, error) {
			asked = append(asked, parent)
			if parent == rings[1].Name {
				return 0, errors.New("permission denied")
			}
			return 3, nil
		})
		require.Len(t, got, 2)
		assert.Equal(t, []string{rings[0].Name, rings[1].Name}, asked, "ListCryptoKeys runs with the ring as parent")
		assert.Equal(t, rings[0].Name, got[0]["name"], "ring keeps its protojson-shaped fields")
		assert.Equal(t, 3, got[0]["numKeys"])
		assert.NotContains(t, got[1], "numKeys", "a failed count must leave numKeys out, not report 0")
	})

	t.Run("FanOutCap", func(t *testing.T) {
		t.Parallel()
		rings := make([]*kmspb.KeyRing, kmsKeyCountFanOutCap+2)
		for i := range rings {
			rings[i] = &kmspb.KeyRing{Name: fmt.Sprintf("projects/p/locations/global/keyRings/r%d", i)}
		}
		calls := 0
		got := keyRingsWithKeyCounts(context.Background(), rings, func(context.Context, string) (int, error) {
			calls++
			return 1, nil
		})
		require.Len(t, got, len(rings), "rings past the cap are still returned")
		assert.Equal(t, kmsKeyCountFanOutCap, calls)
		assert.Contains(t, got[kmsKeyCountFanOutCap-1], "numKeys")
		assert.NotContains(t, got[kmsKeyCountFanOutCap], "numKeys")
	})

	t.Run("Empty_MarshalsAsArray", func(t *testing.T) {
		t.Parallel()
		got := keyRingsWithKeyCounts(context.Background(), nil, func(context.Context, string) (int, error) {
			t.Fatal("no ring, no count")
			return 0, nil
		})
		b, err := json.Marshal(got)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(b), "empty list-keyrings must marshal as [] not null (#255)")
	})
}
