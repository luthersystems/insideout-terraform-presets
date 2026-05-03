package gcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/identitytoolkit/v2"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// fakeIdentityToolkitREST stands in for the Identity Toolkit v2 REST
// endpoint. identitytoolkit.NewService is a googleapi REST client and
// honors option.WithEndpoint.
func fakeIdentityToolkitREST(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []option.ClientOption) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, []option.ClientOption{
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	}
}

func TestInspectIdentityPlatform_ListTenants(t *testing.T) {
	t.Parallel()
	srv, opts := fakeIdentityToolkitREST(t, func(w http.ResponseWriter, r *http.Request) {
		// list-tenants path: /v2/projects/<project>/tenants
		if !strings.Contains(r.URL.Path, "/tenants") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "tenants": [
		    {"name":"projects/demo-proj/tenants/tenant-1","displayName":"Tenant One"},
		    {"name":"projects/demo-proj/tenants/tenant-2","displayName":"Tenant Two"}
		  ]
		}`))
	})
	defer srv.Close()

	got, err := inspectIdentityPlatform(context.Background(), "demo-proj", "list-tenants", "", opts...)
	require.NoError(t, err)

	tenants, ok := got.([]*identitytoolkit.GoogleCloudIdentitytoolkitAdminV2Tenant)
	require.True(t, ok, "expected []*Tenant slice, got %T", got)
	require.Len(t, tenants, 2)
	assert.Equal(t, "Tenant One", tenants[0].DisplayName)
}

// TestInspectIdentityPlatform_ListProvidersBothHalvesSucceed verifies
// the list-providers handler attempts BOTH OAuth IDP configs and
// default-supported IDP configs. Mirrors the contract that the inline
// error-tolerant branch path returns a map with both keys present.
func TestInspectIdentityPlatform_ListProvidersBothHalvesSucceed(t *testing.T) {
	t.Parallel()
	srv, opts := fakeIdentityToolkitREST(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/oauthIdpConfigs"):
			_, _ = w.Write([]byte(`{"oauthIdpConfigs":[{"name":"projects/p/oauthIdpConfigs/oidc-1"}]}`))
		case strings.Contains(r.URL.Path, "/defaultSupportedIdpConfigs"):
			_, _ = w.Write([]byte(`{"defaultSupportedIdpConfigs":[{"name":"projects/p/defaultSupportedIdpConfigs/google.com"}]}`))
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
		}
	})
	defer srv.Close()

	got, err := inspectIdentityPlatform(context.Background(), "demo-proj", "list-providers", "", opts...)
	require.NoError(t, err)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.NotNil(t, m["oauth_idp_configs"], "OAuth IDP configs must surface")
	assert.NotNil(t, m["default_supported_idp_configs"], "default-supported IDP configs must surface")
	assert.NotContains(t, m, "oauth_idp_configs_error")
	assert.NotContains(t, m, "default_supported_idp_configs_error")
}

// TestInspectIdentityPlatform_ListProvidersOAuthFailsContinuesWithDefaults
// — half-failure must surface the error inline AND the working half's
// data. The handler explicitly attempts both halves independently.
func TestInspectIdentityPlatform_ListProvidersOAuthFailsContinuesWithDefaults(t *testing.T) {
	t.Parallel()
	srv, opts := fakeIdentityToolkitREST(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/oauthIdpConfigs"):
			http.Error(w, "permission denied for OAuth", http.StatusForbidden)
		case strings.Contains(r.URL.Path, "/defaultSupportedIdpConfigs"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"defaultSupportedIdpConfigs":[{"name":"projects/p/defaultSupportedIdpConfigs/google.com"}]}`))
		default:
			http.Error(w, "unexpected path", http.StatusInternalServerError)
		}
	})
	defer srv.Close()

	got, err := inspectIdentityPlatform(context.Background(), "demo-proj", "list-providers", "", opts...)
	require.NoError(t, err, "half-failure must NOT abort the call")
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.NotNil(t, m["oauth_idp_configs_error"], "OAuth half failure must surface inline")
	assert.NotNil(t, m["default_supported_idp_configs"], "working half must still surface")
}

func TestInspectIdentityPlatform_UnsupportedAction(t *testing.T) {
	t.Parallel()
	srv, opts := fakeIdentityToolkitREST(t, func(_ http.ResponseWriter, _ *http.Request) {
		// Should never be hit — the action switch fires before any RPC.
	})
	defer srv.Close()
	_, err := inspectIdentityPlatform(context.Background(), "demo-proj", "no-such", "", opts...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Identity Platform action")
}

// TestInspectIdentityPlatform_ListTenants_MultitenancyDisabledReturnsStructuredError
// pins #245: the /v2/projects/{p}/tenants endpoint returns 400
// INVALID_PROJECT_ID when multi-tenancy hasn't been provisioned on
// the project (the API IS enabled and other Identity Platform
// endpoints work fine on the same project). The handler must wrap
// that signature in observability.GCPFeatureNotEnabledError so
// reliable's panel renderer can render a clean empty state via
// errors.As instead of leaking the raw 400 string into the UI.
func TestInspectIdentityPlatform_ListTenants_MultitenancyDisabledReturnsStructuredError(t *testing.T) {
	t.Parallel()
	srv, opts := fakeIdentityToolkitREST(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/tenants") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{
		  "error": {
		    "code": 400,
		    "message": "INVALID_PROJECT_ID",
		    "status": "INVALID_ARGUMENT"
		  }
		}`))
	})
	defer srv.Close()

	_, err := inspectIdentityPlatform(context.Background(), "diagramtest2025-09-14", "list-tenants", "", opts...)
	require.Error(t, err)

	var feErr *observability.GCPFeatureNotEnabledError
	require.True(t, errors.As(err, &feErr),
		"err must wrap GCPFeatureNotEnabledError so reliable can errors.As it; got %T (%v)", err, err)
	assert.Equal(t, "identity_platform_multitenancy", feErr.Feature)
	assert.Equal(t, "diagramtest2025-09-14", feErr.ProjectID)
	require.NotNil(t, feErr.Cause, "Cause must preserve the upstream googleapi error for diagnostics")
}

// TestInspectIdentityPlatform_ListTenants_OtherErrorsPassThrough — the
// structured envelope is precise: only the INVALID_PROJECT_ID 400
// from the tenants endpoint gets wrapped. Generic 5xx / 403 errors
// propagate as-is so callers can distinguish "feature not provisioned"
// from "transient API failure".
func TestInspectIdentityPlatform_ListTenants_OtherErrorsPassThrough(t *testing.T) {
	t.Parallel()
	srv, opts := fakeIdentityToolkitREST(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"backend unavailable","status":"INTERNAL"}}`))
	})
	defer srv.Close()

	_, err := inspectIdentityPlatform(context.Background(), "demo-proj", "list-tenants", "", opts...)
	require.Error(t, err)
	var feErr *observability.GCPFeatureNotEnabledError
	assert.False(t, errors.As(err, &feErr), "5xx errors must NOT be wrapped as feature-not-enabled")
}

// TestInspectIdentityPlatform_ListProviders_INVALIDPROJECTIDNotWrapped
// pins the action-scoping contract: the
// GCPFeatureNotEnabledError wrap is gated to the list-tenants
// branch only. If an unrelated action (e.g. list-providers) ever
// returns 400 INVALID_PROJECT_ID, that's a different failure class
// (truly bad project ID, missing IAM, etc.) and must propagate as
// the raw error so the caller can diagnose. Without this test, a
// future refactor that hoisted the wrap to a shared error path
// would silently swallow real caller bugs as
// "feature_not_enabled".
func TestInspectIdentityPlatform_ListProviders_INVALIDPROJECTIDNotWrapped(t *testing.T) {
	t.Parallel()
	srv, opts := fakeIdentityToolkitREST(t, func(w http.ResponseWriter, r *http.Request) {
		// Both halves of list-providers (oauthIdpConfigs and
		// defaultSupportedIdpConfigs) return the same 400 body. The
		// list-providers handler folds errors into an inline map, so
		// success of THIS test means the per-half error string lands
		// inline AND the outer call is not wrapped.
		_ = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"INVALID_PROJECT_ID","status":"INVALID_ARGUMENT"}}`))
	})
	defer srv.Close()

	got, err := inspectIdentityPlatform(context.Background(), "diagramtest2025-09-14", "list-providers", "", opts...)
	// list-providers tolerates per-half failure inline (it does
	// not surface the call as an error), so err is nil here. The
	// key invariant is that NO GCPFeatureNotEnabledError is
	// constructed for list-providers paths.
	require.NoError(t, err)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.NotNil(t, m["oauth_idp_configs_error"], "OAuth half error must surface inline (not wrapped)")
	assert.NotNil(t, m["default_supported_idp_configs_error"], "default-supported half error must surface inline (not wrapped)")
}
