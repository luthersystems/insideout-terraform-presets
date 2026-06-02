package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_identity_platform_config.
//
// Identity Platform's `Config` is a project-scoped singleton (exactly
// one per project, named `projects/<p>/config`) and isn't surfaced by
// Cloud Asset Inventory's SearchAllResources. The discoverer calls
// identitytoolkit.googleapis.com/v2/projects/<p>/config via
// gcpIdentityPlatformConfigLister, returning zero rows when Identity
// Platform isn't activated on the project (the lister returns
// nil-without-error in that case — see GetIdentityPlatformConfig
// docstring for the NotFound semantics).
//
// Terraform import ID: <project> per the provider:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/identity_platform_config#import

const (
	identityPlatformConfigTFType    = "google_identity_platform_config"
	identityPlatformConfigAssetType = "identitytoolkit.googleapis.com/Config" // descriptive; not in CAI
	identityPlatformConfigService   = "identitytoolkit.googleapis.com"
)

type identityPlatformConfigDiscoverer struct {
	lister gcpIdentityPlatformConfigLister
}

func newIdentityPlatformConfigDiscoverer(lister gcpIdentityPlatformConfigLister) Discoverer {
	return &identityPlatformConfigDiscoverer{lister: lister}
}

func (identityPlatformConfigDiscoverer) ResourceType() string {
	return identityPlatformConfigTFType
}
func (identityPlatformConfigDiscoverer) AssetType() string      { return identityPlatformConfigAssetType }
func (identityPlatformConfigDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (identityPlatformConfigDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (identityPlatformConfigDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("identity_platform_config: dep-chase by ID not supported for non-CAI singletons: %w", ErrNotSupported)
}

// ListNonCAI fetches the project singleton. Returns zero rows when
// Identity Platform isn't activated on the project (per the lister
// contract — a 404 from GetConfig isn't an error, it's an empty
// state). stackProject is ignored: there's one Config per project and
// it doesn't carry the stack-project name in its short name.
func (d *identityPlatformConfigDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, _ []imported.ImportedResource, _ progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	cfg, err := d.lister.GetIdentityPlatformConfig(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	book := addressBook{}
	// Singleton import-ID is just the project ID per provider docs.
	importID := projectID
	assetName := "//identitytoolkit.googleapis.com/" + cfg.Name
	return []imported.ImportedResource{
		makeImportedResource(book, identityPlatformConfigTFType, "config", importID, projectID, "", map[string]string{
			"asset_name": assetName,
			"service":    identityPlatformConfigService,
		}, nil),
	}, nil
}
