package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_storage_bucket.
//
// Cloud Asset Inventory: storage.googleapis.com/Bucket
// Asset name shape:      //storage.googleapis.com/<bucket-name>
// Terraform import ID:   <bucket-name>
//
// GCS bucket names are globally unique, so the import ID is the bare
// name with no project / location qualifier — different from every
// other GCP resource type. Buckets do carry a `location` (region or
// multi-region), which we surface on Identity.Location for downstream
// composer wiring; it's not part of the import ID though.

const (
	storageBucketTFType    = "google_storage_bucket"
	storageBucketAssetType = "storage.googleapis.com/Bucket"

	storageAssetHost = "storage.googleapis.com"
)

type storageBucketDiscoverer struct{}

func newStorageBucketDiscoverer() Discoverer { return &storageBucketDiscoverer{} }

func (storageBucketDiscoverer) ResourceType() string   { return storageBucketTFType }
func (storageBucketDiscoverer) AssetType() string      { return storageBucketAssetType }
func (storageBucketDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (storageBucketDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	return makeImportedResource(book, storageBucketTFType, name, name, projectID, a.Location, map[string]string{
		"asset_name": a.Name,
		"self_link":  fmt.Sprintf("https://www.googleapis.com/storage/v1/b/%s", name),
	}, a.Labels)
}

func (storageBucketDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := storageBucketNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	// Location is unknown without a re-query; leave empty rather than
	// guessing. The driftfix loop will surface it via terraform plan
	// if downstream resources reference it. Tags are nil — DiscoverByID
	// does not re-fetch labels from Cloud Asset.
	return makeImportedResource(addressBook{}, storageBucketTFType, name, name, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/%s", storageAssetHost, name),
		"self_link":  fmt.Sprintf("https://www.googleapis.com/storage/v1/b/%s", name),
	}, nil), nil
}

// storageBucketNameFromID extracts the bucket name from one of three
// accepted inputs: a Cloud Asset full resource name
// (//storage.googleapis.com/<name>), a self-link
// (https://www.googleapis.com/storage/v1/b/<name>), or the bare bucket
// name. Anything else returns ErrNotSupported.
func storageBucketNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("storage_bucket: empty id: %w", ErrNotSupported)
	}
	if strings.HasPrefix(id, "//"+storageAssetHost+"/") {
		return id[len("//"+storageAssetHost+"/"):], nil
	}
	if idx := strings.Index(id, "/b/"); idx >= 0 {
		rest := id[idx+len("/b/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("storage_bucket: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
