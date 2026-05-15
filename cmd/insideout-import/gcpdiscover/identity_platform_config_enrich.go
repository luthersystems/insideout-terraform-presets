package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/googleapi"
	identitytoolkitv2 "google.golang.org/api/identitytoolkit/v2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// identityPlatformConfigEnricher implements AttributeEnricher AND
// ByIDEnricher for google_identity_platform_config. Pairs with
// identityPlatformConfigDiscoverer.
//
// Identity Platform's Config is a project-scoped singleton named
// `projects/<p>/config`. GetConfig takes that fully-qualified name. The
// discoverer puts the ImportID = projectID (per provider import shape),
// so the enricher reconstructs the full name from c.ProjectID at call
// time.
//
// Mapping covers the field families exposed in the provider's resource
// schema (sign_in, notification, monitoring, multi_tenant, blocking_functions,
// authorized_domains, autodelete_anonymous_users). Some nested blocks
// have a deeper structure than we map exhaustively (e.g. the SDK's
// SignInConfig.HashConfig has six fields, the SDK's Notification has
// many sub-templates) — those are left for a follow-up enrichgen pass
// since the field count grows quickly. The mapper covers the
// commonly-set top-level blocks; partial coverage > no coverage.
type identityPlatformConfigEnricher struct {
	fetch func(ctx context.Context, svc *identitytoolkitv2.Service, name string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, error)
}

func newIdentityPlatformConfigEnricher() AttributeEnricher {
	return &identityPlatformConfigEnricher{fetch: defaultIdentityPlatformConfigFetch}
}

var (
	_ AttributeEnricher = (*identityPlatformConfigEnricher)(nil)
	_ ByIDEnricher      = (*identityPlatformConfigEnricher)(nil)
)

func (identityPlatformConfigEnricher) ResourceType() string { return identityPlatformConfigTFType }

func (e identityPlatformConfigEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e identityPlatformConfigEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("identity_platform_config: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e identityPlatformConfigEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.IdentityToolkit == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("identity_platform_config: EnrichClients.ProjectID required (singleton resource name is projects/<p>/config)")
	}
	// Identity Platform's Config is a project-scoped singleton: the
	// provider's import shape is the project ID itself. When the caller
	// supplies both an Identity.ImportID and a Clients.ProjectID they
	// MUST agree — otherwise we'd silently fetch the wrong project's
	// config and write it under this resource's identity. Surface the
	// mismatch instead of guessing.
	if id != nil && id.ImportID != "" && id.ImportID != c.ProjectID {
		return nil, fmt.Errorf("identity_platform_config: project mismatch: Identity.ImportID=%q vs EnrichClients.ProjectID=%q", id.ImportID, c.ProjectID)
	}
	fullName := fmt.Sprintf("projects/%s/config", c.ProjectID)
	cfg, err := e.fetch(ctx, c.IdentityToolkit, fullName)
	if err != nil {
		if isIdentityToolkitNotFound(err) {
			return nil, fmt.Errorf("identity_platform_config: %s: %w", fullName, ErrNotFound)
		}
		return nil, fmt.Errorf("identity_platform_config: get %s: %w", fullName, err)
	}
	typed := mapIdentityPlatformConfig(cfg, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("identity_platform_config: marshal Attrs: %w", err)
	}
	return raw, nil
}

func defaultIdentityPlatformConfigFetch(ctx context.Context, svc *identitytoolkitv2.Service, name string) (*identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, error) {
	return svc.Projects.GetConfig(name).Context(ctx).Do()
}

func isIdentityToolkitNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapIdentityPlatformConfig converts the SDK Config struct into the
// typed Layer-1 model. Coverage is deliberately partial — the deeper
// SignIn / Notification / Mfa sub-structures have many fields that
// rarely round-trip cleanly without a full enrichgen pass.
func mapIdentityPlatformConfig(c *identitytoolkitv2.GoogleCloudIdentitytoolkitAdminV2Config, projectID string) *generated.GoogleIdentityPlatformConfig {
	out := &generated.GoogleIdentityPlatformConfig{}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if c == nil {
		return out
	}
	if c.AutodeleteAnonymousUsers {
		out.AutodeleteAnonymousUsers = generated.LiteralOf(true)
	}
	if len(c.AuthorizedDomains) > 0 {
		domains := make([]*generated.Value[string], 0, len(c.AuthorizedDomains))
		for _, d := range c.AuthorizedDomains {
			domains = append(domains, generated.LiteralOf(d))
		}
		out.AuthorizedDomains = domains
	}
	if c.MultiTenant != nil {
		mt := generated.GoogleIdentityPlatformConfigMultiTenant{}
		populated := false
		if c.MultiTenant.AllowTenants {
			mt.AllowTenants = generated.LiteralOf(true)
			populated = true
		}
		if c.MultiTenant.DefaultTenantLocation != "" {
			mt.DefaultTenantLocation = generated.LiteralOf(c.MultiTenant.DefaultTenantLocation)
			populated = true
		}
		if populated {
			out.MultiTenant = []generated.GoogleIdentityPlatformConfigMultiTenant{mt}
		}
	}
	if c.Monitoring != nil && c.Monitoring.RequestLogging != nil {
		rl := generated.GoogleIdentityPlatformConfigMonitoringRequestLogging{}
		if c.Monitoring.RequestLogging.Enabled {
			rl.Enabled = generated.LiteralOf(true)
		}
		mon := generated.GoogleIdentityPlatformConfigMonitoring{
			RequestLogging: []generated.GoogleIdentityPlatformConfigMonitoringRequestLogging{rl},
		}
		out.Monitoring = []generated.GoogleIdentityPlatformConfigMonitoring{mon}
	}
	if c.SignIn != nil {
		si := generated.GoogleIdentityPlatformConfigSignIn{}
		populated := false
		if c.SignIn.AllowDuplicateEmails {
			si.AllowDuplicateEmails = generated.LiteralOf(true)
			populated = true
		}
		if c.SignIn.Anonymous != nil {
			anon := generated.GoogleIdentityPlatformConfigSignInAnonymous{}
			if c.SignIn.Anonymous.Enabled {
				anon.Enabled = generated.LiteralOf(true)
			}
			si.Anonymous = []generated.GoogleIdentityPlatformConfigSignInAnonymous{anon}
			populated = true
		}
		if c.SignIn.Email != nil {
			email := generated.GoogleIdentityPlatformConfigSignInEmail{}
			if c.SignIn.Email.Enabled {
				email.Enabled = generated.LiteralOf(true)
			}
			if c.SignIn.Email.PasswordRequired {
				email.PasswordRequired = generated.LiteralOf(true)
			}
			si.Email = []generated.GoogleIdentityPlatformConfigSignInEmail{email}
			populated = true
		}
		if c.SignIn.PhoneNumber != nil {
			phone := generated.GoogleIdentityPlatformConfigSignInPhoneNumber{}
			if c.SignIn.PhoneNumber.Enabled {
				phone.Enabled = generated.LiteralOf(true)
			}
			si.PhoneNumber = []generated.GoogleIdentityPlatformConfigSignInPhoneNumber{phone}
			populated = true
		}
		if populated {
			out.SignIn = []generated.GoogleIdentityPlatformConfigSignIn{si}
		}
	}
	return out
}
