package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_secret_manager_secret_version
// (Bundle G3, #475).
//
// Secret versions aren't surfaced by Cloud Asset Inventory's
// SearchAllResources — they live one level below the
// secretmanager.googleapis.com/Secret asset type. The discoverer fans
// out across the google_secret_manager_secret rows discovered during
// the CAI phase, calling
// secretmanager.googleapis.com/v1/projects/<p>/secrets/<s>/versions
// per parent secret via gcpSecretVersionLister.
//
// Terraform import ID:
//
//	projects/<project>/secrets/<secret_id>/versions/<version>
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/secret_manager_secret_version#import
//
// Per-parent failures soft-fail through the progress emitter so a
// single inaccessible secret doesn't drop versions on the rest.
// Secrets rarely hold more than a handful of versions, so there's no
// truncation cap here (compare storage_bucket_object).

const (
	secretManagerSecretVersionTFType    = "google_secret_manager_secret_version"
	secretManagerSecretVersionAssetType = "secretmanager.googleapis.com/SecretVersion" // descriptive only; CAI rejects this
)

type secretManagerSecretVersionDiscoverer struct {
	lister gcpSecretVersionLister
}

func newSecretManagerSecretVersionDiscoverer(lister gcpSecretVersionLister) Discoverer {
	return &secretManagerSecretVersionDiscoverer{lister: lister}
}

func (secretManagerSecretVersionDiscoverer) ResourceType() string {
	return secretManagerSecretVersionTFType
}
func (secretManagerSecretVersionDiscoverer) AssetType() string {
	return secretManagerSecretVersionAssetType
}
func (secretManagerSecretVersionDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (secretManagerSecretVersionDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (secretManagerSecretVersionDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("secret_manager_secret_version: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI walks priorResults for google_secret_manager_secret rows
// and queries each secret's versions. Per-parent failures soft-fail
// via a ServiceWarn — mirrors the sql_user precedent so the UI's
// progress stream sees the same signal stderr would (#396).
func (d *secretManagerSecretVersionDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != secretManagerSecretTFType {
			continue
		}
		secretFullName := prior.Identity.ImportID
		secretShort := prior.Identity.NameHint
		versions, err := d.lister.ListSecretVersions(ctx, projectID, secretFullName)
		if err != nil {
			msg := fmt.Sprintf("secret_manager_secret_version: list failed for secret %q in project %q (continuing): %v", secretFullName, projectID, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, v := range versions {
			importID := secretManagerSecretVersionImportID(secretFullName, v.Version)
			name := secretShort + "-v" + v.Version
			out = append(out, makeImportedResource(book, secretManagerSecretVersionTFType, name, importID, projectID, "", map[string]string{
				"secret":  secretFullName,
				"version": v.Version,
				"state":   v.State,
			}, nil))
		}
	}
	return out, nil
}

// secretManagerSecretVersionImportID composes the Terraform import-ID
// per provider docs:
//
//	projects/<p>/secrets/<id>/versions/<v>
func secretManagerSecretVersionImportID(secretFullName, version string) string {
	return secretFullName + "/versions/" + version
}
