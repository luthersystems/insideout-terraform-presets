package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	identitytoolkitv2 "google.golang.org/api/identitytoolkit/v2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// identityPlatformDefaultSupportedIdpConfigEnricher implements
// AttributeEnricher AND ByIDEnricher for
// google_identity_platform_default_supported_idp_config. Pairs with
// identityPlatformDefaultSupportedIdpConfigDiscoverer (a fan-out non-CAI
// discoverer that surfaces one ImportedResource per (project, idp_id)
// pair under a project whose Identity Platform Config singleton is
// active).
//
// Hand-rolled because Default Supported IDP Configs live one level below
// the Identity Platform Config singleton and aren't surfaced by Cloud
// Asset Inventory's SearchAllResources, so the cloudAssetEnricher
// HYBRID path can't reach them. The SDK Get takes a fully-qualified
// resource name:
//
//	projects/<p>/defaultSupportedIdpConfigs/<idpId>
//
// The discoverer encodes the idp id in NativeIDs["idp_id"] and the
// project ID on Identity.ProjectID; the enricher recomposes the full
// name from those (or falls back to ImportID which has the same shape).
//
// Sensitive field posture: client_id and client_secret are Required by
// the provider schema but the IdentityToolkit SDK's Get returns them
// only when configured. Per decision #36, the enricher writes whatever
// the API returns; the emit/persist layers are responsible for
// redaction at write time. The Layer 2 policy marks both as
// EditNever + Hidden + Redacted via tagPolicy() so they don't leak into
// chat / UI.
type identityPlatformDefaultSupportedIdpConfigEnricher struct {
	fetch func(ctx context.Context, svc *identitytoolkitv2.Service, name string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error)
}

func newIdentityPlatformDefaultSupportedIdpConfigEnricher() AttributeEnricher {
	return &identityPlatformDefaultSupportedIdpConfigEnricher{fetch: defaultIdentityPlatformDefaultSupportedIdpConfigFetch}
}

var (
	_ AttributeEnricher = (*identityPlatformDefaultSupportedIdpConfigEnricher)(nil)
	_ ByIDEnricher      = (*identityPlatformDefaultSupportedIdpConfigEnricher)(nil)
)

func (identityPlatformDefaultSupportedIdpConfigEnricher) ResourceType() string {
	return identityPlatformDefaultSupportedIdpConfigTFType
}

func (e identityPlatformDefaultSupportedIdpConfigEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e identityPlatformDefaultSupportedIdpConfigEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("identity_platform_default_supported_idp_config: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

func (e identityPlatformDefaultSupportedIdpConfigEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.IdentityToolkit == nil {
		return nil, ErrEnrichClientUnavailable
	}
	full := identityPlatformDefaultSupportedIdpConfigFullNameForEnrich(id)
	if full == "" {
		return nil, fmt.Errorf("identity_platform_default_supported_idp_config: cannot derive resource name from Identity (Address=%q ImportID=%q ProjectID=%q NativeIDs.idp_id=%q)",
			id.Address, id.ImportID, id.ProjectID, id.NativeIDs["idp_id"])
	}
	v, err := e.fetch(ctx, c.IdentityToolkit, full)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("identity_platform_default_supported_idp_config: %s: %w", full, ErrNotFound)
		}
		return nil, fmt.Errorf("identity_platform_default_supported_idp_config: get %q: %w", full, err)
	}
	typed := mapIdentityPlatformDefaultSupportedIdpConfig(v, id.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("identity_platform_default_supported_idp_config: marshal Attrs: %w", err)
	}
	return raw, nil
}

// identityPlatformDefaultSupportedIdpConfigFullNameForEnrich rebuilds
// the fully-qualified `projects/<p>/defaultSupportedIdpConfigs/<idpId>`
// form the Get call requires. Precedence:
//
//  1. ProjectID + NativeIDs["idp_id"] — the canonical fields the
//     discoverer populates.
//  2. ImportID, if it already has the `defaultSupportedIdpConfigs`
//     segment (the provider's import shape matches the API name).
//
// Returns "" if neither path yields a complete name.
func identityPlatformDefaultSupportedIdpConfigFullNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if project := id.ProjectID; project != "" {
		if idpID := id.NativeIDs["idp_id"]; idpID != "" {
			return identityPlatformDefaultSupportedIdpConfigImportID(project, idpID)
		}
	}
	if id.ImportID != "" && strings.Contains(id.ImportID, "/defaultSupportedIdpConfigs/") {
		return id.ImportID
	}
	return ""
}

func defaultIdentityPlatformDefaultSupportedIdpConfigFetch(ctx context.Context, svc *identitytoolkitv2.Service, name string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, error) {
	return svc.Projects.DefaultSupportedIdpConfigs.Get(name).Context(ctx).Do()
}

// mapIdentityPlatformDefaultSupportedIdpConfig converts the SDK
// DefaultSupportedIdpConfig response into the typed Layer-1
// *generated.GoogleIdentityPlatformDefaultSupportedIdpConfig model.
//
// Field coverage per the curated Layer-2 policy:
//
//   - idp_id    — Required identity (the canonical "google.com",
//     "facebook.com", "apple.com" key). Recovered from the trailing
//     segment of Name when set.
//   - enabled   — RoleTuning + DriftSemanticExact (the drift hook).
//   - client_id, client_secret — written through, but the policy
//     classifies them as Hidden + Redacted via tagPolicy(). The
//     emit/persist layers redact at write time per decision #36.
//   - project   — propagated from the Identity so downstream tooling
//     can attribute the resource without re-parsing the API name.
//
// Skipped fields:
//
//   - name — Computed-only (decision #5: provider fills on refresh).
//   - id   — Computed-only.
func mapIdentityPlatformDefaultSupportedIdpConfig(b *identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2DefaultSupportedIdpConfig, projectID string) *generated.GoogleIdentityPlatformDefaultSupportedIdpConfig {
	out := &generated.GoogleIdentityPlatformDefaultSupportedIdpConfig{}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if b == nil {
		return out
	}
	if idpID := identityPlatformDefaultSupportedIdpConfigIDFromName(b.Name); idpID != "" {
		out.IdpID = generated.LiteralOf(idpID)
	}
	out.Enabled = generated.LiteralOf(b.Enabled)
	if b.ClientId != "" {
		out.ClientID = generated.LiteralOf(b.ClientId)
	}
	if b.ClientSecret != "" {
		out.ClientSecret = generated.LiteralOf(b.ClientSecret)
	}
	return out
}

// identityPlatformDefaultSupportedIdpConfigIDFromName extracts the
// IDP id (e.g. "google.com") from the fully-qualified resource name
// `projects/<p>/defaultSupportedIdpConfigs/<idpId>`. Returns "" if the
// shape doesn't match.
func identityPlatformDefaultSupportedIdpConfigIDFromName(full string) string {
	const sep = "/defaultSupportedIdpConfigs/"
	if i := strings.Index(full, sep); i >= 0 {
		return full[i+len(sep):]
	}
	return ""
}
