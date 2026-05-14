package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_storage_bucket_iam_member (Bundle G1, #470).
//
// Walks the google_storage_bucket rows discovered during the CAI phase
// and calls storage.googleapis.com/v1 Buckets.GetIamPolicy per bucket,
// emitting one row per (bucket × role × member). Per-bucket failures
// soft-fail via the progress emitter so a single bucket without
// storage.buckets.getIamPolicy permission doesn't drop the rest.
//
// Terraform import ID: "b/<bucket> <role> <member>" (space-separated,
// with the "b/" prefix on the bucket). Pinned to the live provider
// docs:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/storage_bucket_iam#google_storage_bucket_iam_member

const (
	storageBucketIAMMemberTFType    = "google_storage_bucket_iam_member"
	storageBucketIAMMemberAssetType = "storage.googleapis.com/IamPolicy" // descriptive only
)

type storageBucketIAMMemberDiscoverer struct {
	lister gcpIAMPolicyLister
}

func newStorageBucketIAMMemberDiscoverer(lister gcpIAMPolicyLister) Discoverer {
	return &storageBucketIAMMemberDiscoverer{lister: lister}
}

func (storageBucketIAMMemberDiscoverer) ResourceType() string   { return storageBucketIAMMemberTFType }
func (storageBucketIAMMemberDiscoverer) AssetType() string      { return storageBucketIAMMemberAssetType }
func (storageBucketIAMMemberDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (storageBucketIAMMemberDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (storageBucketIAMMemberDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("storage_bucket_iam_member: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

func (d *storageBucketIAMMemberDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
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
		bindings, err := d.lister.GetBucketIAMPolicy(ctx, bucket)
		if err != nil {
			msg := fmt.Sprintf("storage_bucket_iam_member: get IAM policy failed for bucket %q (continuing): %v", bucket, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, b := range bindings {
			for _, m := range b.Members {
				importID := storageBucketIAMMemberImportID(bucket, b.Role, m)
				name := bucket + "-" + iamRoleSuffix(b.Role) + "-" + iamMemberSuffix(m)
				out = append(out, makeImportedResource(book, storageBucketIAMMemberTFType, name, importID, projectID, "", map[string]string{
					"bucket": bucket,
					"role":   b.Role,
					"member": m,
				}, nil))
			}
		}
	}
	return out, nil
}

// storageBucketIAMMemberImportID composes the Terraform import-ID per
// provider docs: "b/<bucket> <role> <member>" — the "b/" prefix is
// required so the provider's parser routes the resource ID to the
// bucket-IAM-member shape rather than a bare bucket name.
func storageBucketIAMMemberImportID(bucket, role, member string) string {
	return "b/" + bucket + " " + role + " " + member
}
