package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_identity_platform_default_supported_idp_config
// (Bundle G4, #478).
//
// Default-supported IDP configs (Google, Facebook, Twitter, Apple, etc.)
// live one level below the project-scoped Identity Platform Config
// singleton (#392 / google_identity_platform_config). They aren't
// surfaced by Cloud Asset Inventory's SearchAllResources, so the
// discoverer lives in the non-CAI bucket and fans out from any
// google_identity_platform_config priorResults rows discovered during
// the non-CAI phase. When Identity Platform isn't activated on the
// project the parent yields zero rows and this discoverer naturally
// emits nothing — the fan-out guard matches the sql_user / G3
// sub-resource precedents.
//
// Terraform import ID:
//
//	projects/<project>/defaultSupportedIdpConfigs/<idpId>
//
// per the provider's documentation:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/google_identity_platform_default_supported_idp_config#import

const (
	identityPlatformDefaultSupportedIdpConfigTFType    = "google_identity_platform_default_supported_idp_config"
	identityPlatformDefaultSupportedIdpConfigAssetType = "identitytoolkit.googleapis.com/DefaultSupportedIdpConfig" // descriptive only; CAI rejects this
)

type identityPlatformDefaultSupportedIdpConfigDiscoverer struct {
	lister gcpDefaultSupportedIdpConfigLister
}

func newIdentityPlatformDefaultSupportedIdpConfigDiscoverer(lister gcpDefaultSupportedIdpConfigLister) Discoverer {
	return &identityPlatformDefaultSupportedIdpConfigDiscoverer{lister: lister}
}

func (identityPlatformDefaultSupportedIdpConfigDiscoverer) ResourceType() string {
	return identityPlatformDefaultSupportedIdpConfigTFType
}

func (identityPlatformDefaultSupportedIdpConfigDiscoverer) AssetType() string {
	return identityPlatformDefaultSupportedIdpConfigAssetType
}

func (identityPlatformDefaultSupportedIdpConfigDiscoverer) ScopeStyle() ScopeStyle {
	return ScopeStyleNonCAI
}

func (identityPlatformDefaultSupportedIdpConfigDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (identityPlatformDefaultSupportedIdpConfigDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("identity_platform_default_supported_idp_config: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI fans out across priorResults for the singleton
// google_identity_platform_config row. When the parent isn't present
// (project hasn't activated Identity Platform) the discoverer emits
// zero rows. If the lister call itself fails the error is surfaced —
// unlike the SQL user / IAM precedents there is no per-parent fan-out
// where soft-fail makes sense; Identity Platform is a singleton, so a
// failure here is project-wide and operators want to see it.
func (d *identityPlatformDefaultSupportedIdpConfigDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, _ progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	// Only call the lister if the project surfaces an Identity Platform
	// Config singleton row. Without that gate every project would
	// receive a probe call for default IDP configs even when Identity
	// Platform isn't activated; the GCP API returns a hard error in
	// that case, not an empty list, so the gate is load-bearing.
	hasParent := false
	for _, prior := range priorResults {
		if prior.Identity.Type == identityPlatformConfigTFType {
			hasParent = true
			break
		}
	}
	if !hasParent {
		return nil, nil
	}
	configs, err := d.lister.ListDefaultSupportedIdpConfigs(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return nil, nil
	}
	book := addressBook{}
	out := make([]imported.ImportedResource, 0, len(configs))
	for _, c := range configs {
		idpID := c.IdpID
		if idpID == "" {
			// Recover the IDP id from the full Name path
			// "projects/<p>/defaultSupportedIdpConfigs/<id>" when the
			// lister didn't pre-extract it. Belt-and-braces — the Real
			// lister populates IdpID, but fakes that only set Name
			// shouldn't surface as silent zero-id rows.
			if i := strings.LastIndex(c.Name, "/"); i >= 0 && i < len(c.Name)-1 {
				idpID = c.Name[i+1:]
			}
		}
		if idpID == "" {
			continue
		}
		importID := identityPlatformDefaultSupportedIdpConfigImportID(projectID, idpID)
		out = append(out, makeImportedResource(book, identityPlatformDefaultSupportedIdpConfigTFType, idpID, importID, projectID, "", map[string]string{
			"idp_id":  idpID,
			"enabled": fmt.Sprintf("%t", c.Enabled),
		}, nil))
	}
	return out, nil
}

// identityPlatformDefaultSupportedIdpConfigImportID composes the
// Terraform import-ID per provider docs:
//
//	projects/<project>/defaultSupportedIdpConfigs/<idpId>
func identityPlatformDefaultSupportedIdpConfigImportID(projectID, idpID string) string {
	return "projects/" + projectID + "/defaultSupportedIdpConfigs/" + idpID
}
