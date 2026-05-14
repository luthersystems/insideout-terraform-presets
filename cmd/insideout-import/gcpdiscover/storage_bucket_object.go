package gcpdiscover

import (
	"context"
	"errors"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_storage_bucket_object (Bundle G3, #475).
//
// GCS objects aren't surfaced by Cloud Asset Inventory — only the
// containing storage.googleapis.com/Bucket is. This discoverer fans out
// across the google_storage_bucket rows discovered during the CAI phase
// and lists objects per bucket via gcpBucketObjectLister.
//
// Terraform import ID:
//
//	<bucket>/<object_name>
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/storage_bucket_object#import
//
// **Cardinality risk:** GCS buckets can hold millions of objects. The
// per-bucket enumeration is capped by the lister at
// defaultMaxBucketObjects (1000); when truncation fires, the lister
// returns its partial slice along with errBucketObjectsTruncated and
// the discoverer emits a ServiceWarn so the operator sees the cap was
// hit. Practical reasoning lives on the constant — operators with
// huge buckets generally don't manage individual objects in
// Terraform, and the cap surfaces realistic cases without overwhelming
// the import workflow.
//
// Per-parent failures soft-fail through the progress emitter (same
// pattern as sql_user / G1 IAM discoverers).

const (
	storageBucketObjectTFType    = "google_storage_bucket_object"
	storageBucketObjectAssetType = "storage.googleapis.com/Object" // descriptive only; CAI rejects this
)

type storageBucketObjectDiscoverer struct {
	lister gcpBucketObjectLister
}

func newStorageBucketObjectDiscoverer(lister gcpBucketObjectLister) Discoverer {
	return &storageBucketObjectDiscoverer{lister: lister}
}

func (storageBucketObjectDiscoverer) ResourceType() string   { return storageBucketObjectTFType }
func (storageBucketObjectDiscoverer) AssetType() string      { return storageBucketObjectAssetType }
func (storageBucketObjectDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (storageBucketObjectDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (storageBucketObjectDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("storage_bucket_object: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI walks priorResults for google_storage_bucket rows and
// enumerates objects per bucket. Truncation at the per-bucket cap
// surfaces as a ServiceWarn; the returned slice still includes
// whatever was discovered before the cap fired. Real (non-truncation)
// errors soft-fail per the usual sql_user/IAM pattern.
func (d *storageBucketObjectDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != storageBucketTFType {
			continue
		}
		bucket := prior.Identity.NameHint
		objects, err := d.lister.ListBucketObjects(ctx, bucket)
		switch {
		case errors.Is(err, errBucketObjectsTruncated):
			// Truncation is not a soft-fail — objects[:cap] is
			// still legitimate; just warn so the operator sees they
			// hit the ceiling. Fall through to emit the rows we got.
			msg := fmt.Sprintf("storage_bucket_object: bucket %q has more than %d objects; truncated", bucket, defaultMaxBucketObjects)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
		case err != nil:
			msg := fmt.Sprintf("storage_bucket_object: list failed for bucket %q (continuing): %v", bucket, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, o := range objects {
			importID := storageBucketObjectImportID(bucket, o.Name)
			out = append(out, makeImportedResource(book, storageBucketObjectTFType, storageBucketObjectNameHint(bucket, o.Name), importID, projectID, "", map[string]string{
				"bucket": bucket,
				"name":   o.Name,
				"md5":    o.Md5,
			}, nil))
		}
	}
	return out, nil
}

// storageBucketObjectImportID composes the Terraform import-ID per
// provider docs: "<bucket>/<object_name>". Object names may contain
// slashes themselves (folder-shaped keys); we preserve them verbatim
// because the provider's parser splits on the first slash to extract
// the bucket and treats everything after as the object key.
func storageBucketObjectImportID(bucket, name string) string {
	return bucket + "/" + name
}

// storageBucketObjectNameHint composes a terraform-address-safe name
// hint from the bucket and object name. The composer's GenerateAddress
// already sanitizes the result, but joining bucket + object up-front
// keeps row collisions to a minimum when one stack has multiple
// objects with the same basename across different buckets.
func storageBucketObjectNameHint(bucket, name string) string {
	return bucket + "-" + name
}
