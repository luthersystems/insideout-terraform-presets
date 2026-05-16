package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func storageBucketObjectIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_storage_bucket_object",
		NameHint: "my-bucket-app-config-json",
		Address:  "google_storage_bucket_object.my_bucket_app_config_json",
		ImportID: "my-bucket/app/config.json",
		NativeIDs: map[string]string{
			"bucket": "my-bucket",
			"name":   "app/config.json",
			"md5":    "deadbeef==",
		},
	}
}

func TestStorageBucketObjectEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	e := newStorageBucketObjectEnricher()
	assert.Equal(t, "google_storage_bucket_object", e.ResourceType())
}

func TestStorageBucketObjectEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newStorageBucketObjectEnricher()
	ir := &imported.ImportedResource{Identity: storageBucketObjectIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Storage: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestStorageBucketObjectEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := storageBucketObjectEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, _, _ string) (*storagev1.Object, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound}
		},
	}
	ir := &imported.ImportedResource{Identity: storageBucketObjectIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Storage: &storagev1.Service{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStorageBucketObjectEnrich_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 forbidden")
	e := storageBucketObjectEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, _, _ string) (*storagev1.Object, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: storageBucketObjectIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Storage: &storagev1.Service{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.NotErrorIs(t, err, ErrNotFound, "non-404 must not be reported as ErrNotFound")
}

func TestStorageBucketObjectEnrich_HappyPath(t *testing.T) {
	t.Parallel()
	src := &storagev1.Object{
		Bucket:       "my-bucket",
		Name:         "app/config.json",
		ContentType:  "application/json",
		StorageClass: "STANDARD",
		Crc32c:       "AAAAAA==",
		Md5Hash:      "deadbeef==",
		Generation:   1718000000000001,
		CacheControl: "no-cache",
	}
	var gotBucket, gotObject string
	e := storageBucketObjectEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, bucket, object string) (*storagev1.Object, error) {
			gotBucket = bucket
			gotObject = object
			return src, nil
		},
	}
	ir := &imported.ImportedResource{Identity: storageBucketObjectIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Storage: &storagev1.Service{}}))
	assert.Equal(t, "my-bucket", gotBucket, "fetch must receive the bucket NativeID")
	assert.Equal(t, "app/config.json", gotObject, "fetch must receive the object NativeID, slashes preserved")

	decoded, err := generated.UnmarshalAttrs("google_storage_bucket_object", ir.Attrs)
	require.NoError(t, err)
	v, ok := decoded.(*generated.GoogleStorageBucketObject)
	require.True(t, ok)
	require.NotNil(t, v.Bucket)
	assert.Equal(t, "my-bucket", *v.Bucket.Literal)
	require.NotNil(t, v.Name)
	assert.Equal(t, "app/config.json", *v.Name.Literal)
	require.NotNil(t, v.ContentType)
	assert.Equal(t, "application/json", *v.ContentType.Literal)
	require.NotNil(t, v.StorageClass)
	assert.Equal(t, "STANDARD", *v.StorageClass.Literal)
	require.NotNil(t, v.Crc32c)
	assert.Equal(t, "AAAAAA==", *v.Crc32c.Literal)
	require.NotNil(t, v.Md5hash)
	assert.Equal(t, "deadbeef==", *v.Md5hash.Literal)
	require.NotNil(t, v.Generation)
	assert.Equal(t, float64(1718000000000001), *v.Generation.Literal)
	require.NotNil(t, v.CacheControl)
	assert.Equal(t, "no-cache", *v.CacheControl.Literal)

	// Body must NEVER be populated — metadata Get does not return content.
	assert.Nil(t, v.Content, "object body must never leak — metadata Get does not return it")
	assert.Nil(t, v.Source, "TF-only knob; no API equivalent")

	// Computed-only fields must not leak into Attrs.
	assert.Nil(t, v.ID)
	assert.Nil(t, v.SelfLink)
	assert.Nil(t, v.MediaLink)
	assert.Nil(t, v.OutputName)
}

func TestStorageBucketObjectEnrich_FallsBackToImportIDWhenNativeIDsMissing(t *testing.T) {
	t.Parallel()
	src := &storagev1.Object{
		Bucket: "my-bucket",
		Name:   "nested/path/file.txt",
	}
	var gotBucket, gotObject string
	e := storageBucketObjectEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, bucket, object string) (*storagev1.Object, error) {
			gotBucket = bucket
			gotObject = object
			return src, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_storage_bucket_object",
			ImportID: "my-bucket/nested/path/file.txt",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Storage: &storagev1.Service{}}))
	assert.Equal(t, "my-bucket", gotBucket, "split on FIRST slash extracts bucket")
	assert.Equal(t, "nested/path/file.txt", gotObject, "object name retains internal slashes")
}

func TestStorageBucketObjectEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := storageBucketObjectEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, _, _ string) (*storagev1.Object, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_storage_bucket_object"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Storage: &storagev1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive (bucket, object)")
}

func TestStorageBucketObjectEnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	src := &storagev1.Object{
		Bucket:      "my-bucket",
		Name:        "app/config.json",
		ContentType: "application/json",
	}
	e := storageBucketObjectEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, _, _ string) (*storagev1.Object, error) {
			return src, nil
		},
	}
	id := storageBucketObjectIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{Storage: &storagev1.Service{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &probe))
	assert.Contains(t, probe, "bucket")
	assert.Contains(t, probe, "name")
	assert.Contains(t, probe, "content_type")
}

func TestStorageBucketObjectEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newStorageBucketObjectEnricher().(*storageBucketObjectEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{Storage: &storagev1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestStorageBucketObjectRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_storage_bucket_object"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_storage_bucket_object", enr.ResourceType())
}
