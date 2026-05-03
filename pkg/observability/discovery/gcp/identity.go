// Identity Platform inspector.
//
// Mirrors:
//   - inspectGCPIdentityPlatform — reliable gcp_inspect.go:698
//
// The admin surface for Identity Platform lives in the REST-only SDK at
// google.golang.org/api/identitytoolkit/v2 — the native
// cloud.google.com/go/identitytoolkit SDK only covers end-user auth/MFA
// operations, not project-level tenant or IDP enumeration.
//
// list-tenants paginates server-side and caps the accumulated slice at
// identityPlatformMaxTenants to keep response payloads bounded.
// list-providers returns both the custom OAuth and default-supported
// IDP lists; both halves are attempted independently so a permission
// gap on one flavour doesn't hide the other.
//
// No labels.project filter applies — Tenants and IDP configs have no
// labels in the v2 admin API and are scoped by project at the parent
// path.

package gcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/identitytoolkit/v2"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// identityPlatformMaxTenants bounds the list-tenants response. 1000 is
// the server-side PageSize ceiling; pagination stops accumulating past
// this cap and a warn-log fires so operators can spot under-reporting
// without a shape-breaking envelope change. Mirrors reliable's
// gcp_inspect.go:680.
const identityPlatformMaxTenants = 1000

func inspectIdentityPlatform(ctx context.Context, projectID, action, _ string, opts ...option.ClientOption) (any, error) {
	svc, err := identitytoolkit.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	parent := fmt.Sprintf("projects/%s", projectID)

	switch action {
	case "list-tenants":
		var tenants []*identitytoolkit.GoogleCloudIdentitytoolkitAdminV2Tenant
		var pageToken string
		truncated := false
		for {
			call := svc.Projects.Tenants.List(parent).Context(ctx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			resp, err := call.Do()
			if err != nil {
				if isIdentityPlatformMultitenancyDisabled(err) {
					// Identity Platform multi-tenancy is a per-
					// project opt-in — the API is enabled but the
					// /v2/projects/{p}/tenants endpoint returns 400
					// INVALID_PROJECT_ID until a tenant is first
					// created via the console or REST. Surface a
					// structured envelope so the panel renders a
					// clean "feature not enabled" empty state
					// instead of leaking the raw 400 (#245).
					return nil, observability.NewGCPFeatureNotEnabledError(
						"identity_platform_multitenancy", projectID, err)
				}
				return nil, err
			}
			tenants = append(tenants, resp.Tenants...)
			if len(tenants) >= identityPlatformMaxTenants {
				// NextPageToken may still be non-empty — we're giving
				// up by design, not because the server ran out. Log
				// loudly so an operator seeing "exactly N tenants" on
				// a customer report knows to investigate.
				truncated = resp.NextPageToken != ""
				break
			}
			if resp.NextPageToken == "" {
				break
			}
			pageToken = resp.NextPageToken
		}
		if truncated {
			log.Printf("[discovery/gcp identityplatform] list-tenants TRUNCATED at cap=%d — "+
				"more tenants exist upstream. Raise identityPlatformMaxTenants or paginate via filters if this fires in prod.",
				identityPlatformMaxTenants)
		}
		return tenants, nil

	case "list-providers":
		// Identity Platform OAuth IDPs come in two flavours: the
		// "default-supported" list (Google, Apple, Facebook, Twitter,
		// …) which uses a managed config, and the custom OAuth IDP
		// list (third-party OIDC providers configured by the
		// operator). Attempt both independently so one half's
		// permission/API error doesn't black-hole the other. Errors
		// are surfaced inline in the response instead of
		// short-circuiting — the caller sees what we DID get plus a
		// per-half error string.
		resp := map[string]any{}
		if oauth, oauthErr := svc.Projects.OauthIdpConfigs.List(parent).Context(ctx).Do(); oauthErr != nil {
			log.Printf("[discovery/gcp identityplatform] list-providers oauth_idp_configs error (continuing with defaults): %v", oauthErr)
			resp["oauth_idp_configs_error"] = oauthErr.Error()
		} else {
			resp["oauth_idp_configs"] = oauth.OauthIdpConfigs
		}
		if defaults, defErr := svc.Projects.DefaultSupportedIdpConfigs.List(parent).Context(ctx).Do(); defErr != nil {
			log.Printf("[discovery/gcp identityplatform] list-providers default_supported_idp_configs error: %v", defErr)
			resp["default_supported_idp_configs_error"] = defErr.Error()
		} else {
			resp["default_supported_idp_configs"] = defaults.DefaultSupportedIdpConfigs
		}
		return resp, nil

	default:
		return nil, unsupportedActionError("Identity Platform", action, observability.GCPServiceActions["identityplatform"])
	}
}

// isIdentityPlatformMultitenancyDisabled reports whether err is the
// specific "400 INVALID_PROJECT_ID" signature returned by /v2/
// projects/{p}/tenants when multi-tenancy hasn't been provisioned.
// Other Identity Platform endpoints on the same project return 200
// fine (verified live against diagramtest2025-09-14, #245), so the
// signature is precise enough to dispatch on without false positives.
func isIdentityPlatformMultitenancyDisabled(err error) bool {
	if err == nil {
		return false
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	if gerr.Code != 400 {
		return false
	}
	return strings.Contains(gerr.Message, "INVALID_PROJECT_ID")
}
