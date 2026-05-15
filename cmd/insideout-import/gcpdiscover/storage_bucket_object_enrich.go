package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// storageBucketObjectEnricher implements AttributeEnricher AND
// ByIDEnricher for google_storage_bucket_object. Pairs with
// storageBucketObjectDiscoverer (a fan-out non-CAI discoverer that
// surfaces one ImportedResource per (bucket, object) pair).
//
// Hand-rolled (no .gen.go partner) because GCS objects aren't surfaced
// by Cloud Asset Inventory's SearchAllResources — only the containing
// storage.googleapis.com/Bucket is. The SDK Get takes a (bucket, object)
// pair; the discoverer encodes both in NativeIDs and the enricher
// recomposes them (or falls back to parsing ImportID, which uses the
// "<bucket>/<object_name>" provider import shape).
//
// **Body posture (decision #36):** the enricher NEVER populates
// `content` (the object body). Storage.Objects.Get returns metadata
// only; downloading the body needs a separate `.Download()` call on
// the same handle, which surfaces an io.Reader rather than a struct
// field. Reading object bodies into Layer-1 would defeat the typical
// reason objects exist (binary blobs / large files) and bloats the
// import snapshot for no curation benefit — drift on `content` is
// handled by the carrier's lifecycle.ignore_changes posture in
// genconfig.cleanup, matching the secret_manager_secret_version
// `secret_data` skip.
type storageBucketObjectEnricher struct {
	fetch func(ctx context.Context, svc *storagev1.Service, bucket, object string) (*storagev1.Object, error)
}

func newStorageBucketObjectEnricher() AttributeEnricher {
	return &storageBucketObjectEnricher{fetch: defaultStorageBucketObjectFetch}
}

var (
	_ AttributeEnricher = (*storageBucketObjectEnricher)(nil)
	_ ByIDEnricher      = (*storageBucketObjectEnricher)(nil)
)

func (storageBucketObjectEnricher) ResourceType() string {
	return storageBucketObjectTFType
}

func (e storageBucketObjectEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e storageBucketObjectEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("storage_bucket_object: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

func (e storageBucketObjectEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Storage == nil {
		return nil, ErrEnrichClientUnavailable
	}
	bucket, object := storageBucketObjectKeysForEnrich(id)
	if bucket == "" || object == "" {
		return nil, fmt.Errorf("storage_bucket_object: cannot derive (bucket, object) from Identity (Address=%q ImportID=%q NativeIDs.bucket=%q NativeIDs.name=%q)",
			id.Address, id.ImportID, id.NativeIDs["bucket"], id.NativeIDs["name"])
	}
	obj, err := e.fetch(ctx, c.Storage, bucket, object)
	if err != nil {
		if isStorageObjectNotFound(err) {
			return nil, fmt.Errorf("storage_bucket_object: %s/%s: %w", bucket, object, ErrNotFound)
		}
		return nil, fmt.Errorf("storage_bucket_object: get %s/%s: %w", bucket, object, err)
	}
	typed := mapStorageBucketObject(obj)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("storage_bucket_object: marshal Attrs: %w", err)
	}
	return raw, nil
}

// storageBucketObjectKeysForEnrich pulls the (bucket, object) pair the
// SDK Get takes. Precedence:
//
//  1. NativeIDs["bucket"] + NativeIDs["name"] — the canonical fields
//     the discoverer populates.
//  2. ImportID split on the first "/" — the provider's import shape is
//     "<bucket>/<object_name>". Object names may themselves contain
//     slashes (folder-shaped keys); the provider splits on the FIRST
//     slash so we mirror that.
//
// Returns ("", "") if neither path yields both values.
func storageBucketObjectKeysForEnrich(id *imported.ResourceIdentity) (bucket, object string) {
	if id == nil {
		return "", ""
	}
	if b := id.NativeIDs["bucket"]; b != "" {
		if n := id.NativeIDs["name"]; n != "" {
			return b, n
		}
	}
	if id.ImportID != "" {
		if i := strings.Index(id.ImportID, "/"); i > 0 && i < len(id.ImportID)-1 {
			return id.ImportID[:i], id.ImportID[i+1:]
		}
	}
	return "", ""
}

func defaultStorageBucketObjectFetch(ctx context.Context, svc *storagev1.Service, bucket, object string) (*storagev1.Object, error) {
	return svc.Objects.Get(bucket, object).Context(ctx).Do()
}

// isStorageObjectNotFound mirrors isComputeNotFound / isSecretManagerVersionNotFound:
// 404 from the Storage REST API is the not-found signal. Used so
// EnrichByID returns ErrNotFound (a distinguishable sentinel) instead
// of a generic wrapped error.
func isStorageObjectNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapStorageBucketObject converts a *storagev1.Object into the typed
// Layer-1 *generated.GoogleStorageBucketObject model.
//
// Field coverage per the curated Layer-2 policy:
//
//   - bucket, name — Identity (Required).
//   - content_type, cache_control, content_disposition,
//     content_encoding, content_language — RoleTuning HTTP metadata.
//   - storage_class — RoleTuning Performance.
//   - kms_key_name — RoleWiring Security.
//   - crc32c, md5hash, generation — Computed observability fields.
//
// Skipped fields:
//
//   - content — see header (object body is NEVER read; metadata only).
//   - source, detect_md5hash — TF-only knobs with no API equivalent.
//   - id, self_link, media_link, output_name — Computed-only.
//   - event_based_hold, temporary_hold, customer_encryption, retention,
//     metadata — populated when the SDK returns them, but omitted by
//     default to keep the mapper aligned with the curated policy
//     surface (the bag default is DriftSemanticNone for whole-block
//     fields not explicitly enumerated).
func mapStorageBucketObject(o *storagev1.Object) *generated.GoogleStorageBucketObject {
	out := &generated.GoogleStorageBucketObject{}
	if o == nil {
		return out
	}
	if o.Bucket != "" {
		out.Bucket = generated.LiteralOf(o.Bucket)
	}
	if o.Name != "" {
		out.Name = generated.LiteralOf(o.Name)
	}
	if o.ContentType != "" {
		out.ContentType = generated.LiteralOf(o.ContentType)
	}
	if o.CacheControl != "" {
		out.CacheControl = generated.LiteralOf(o.CacheControl)
	}
	if o.ContentDisposition != "" {
		out.ContentDisposition = generated.LiteralOf(o.ContentDisposition)
	}
	if o.ContentEncoding != "" {
		out.ContentEncoding = generated.LiteralOf(o.ContentEncoding)
	}
	if o.ContentLanguage != "" {
		out.ContentLanguage = generated.LiteralOf(o.ContentLanguage)
	}
	if o.StorageClass != "" {
		out.StorageClass = generated.LiteralOf(o.StorageClass)
	}
	if o.KmsKeyName != "" {
		out.KMSKeyName = generated.LiteralOf(o.KmsKeyName)
	}
	if o.Crc32c != "" {
		out.Crc32c = generated.LiteralOf(o.Crc32c)
	}
	if o.Md5Hash != "" {
		out.Md5hash = generated.LiteralOf(o.Md5Hash)
	}
	if o.Generation != 0 {
		out.Generation = generated.LiteralOf(float64(o.Generation))
	}
	return out
}
