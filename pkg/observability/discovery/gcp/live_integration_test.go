//go:build integration

// Live GCP smoke tests. Run with:
//
//	go test -tags=integration ./pkg/observability/discovery/gcp/... -v -run TestLive
//
// Requires:
//   - Application Default Credentials in the environment (e.g.
//     `gcloud auth application-default login`, or a service-account
//     key path in GOOGLE_APPLICATION_CREDENTIALS).
//   - LIVE_GCP_PROJECT_ID set to a project where the relevant APIs
//     are enabled (Compute, API Gateway, Vertex AI, Identity Toolkit,
//     Firestore — see gcloud services list output).
//   - Optional: LIVE_GCP_FIRESTORE_DB to probe a named Firestore
//     database (the gcp/firestore preset uses a non-default name per
//     issue #159; pass the database_name output of the deployed
//     stack).
//
// Build-tag-gated so CI doesn't exercise these. The TestLive_*
// suite is the live-fire confirmation that complements the unit-
// test pins in network_test.go / data_test.go / identity_test.go.
//
// History: TestLive_ComputeV1FilterRegimes is the lesson from #245.
// Both #239 and #245 shipped because we had unit tests pinning a
// wire format the live API rejected, with no live integration test
// to cross-check. This file is the cross-check; it must run before
// every release that touches discovery/gcp.

package gcp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// liveProjectOrSkip returns the project ID from LIVE_GCP_PROJECT_ID or
// skips the test. ADC is consumed by the SDK clients implicitly.
func liveProjectOrSkip(t *testing.T) string {
	t.Helper()
	projectID := os.Getenv("LIVE_GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("LIVE_GCP_PROJECT_ID not set; export the project ID to run live GCP probes")
	}
	return projectID
}

// gcloudTokenSource returns an oauth2.TokenSource that shells out to
// `gcloud auth print-access-token`. Used as a fallback when ADC is in
// an invalid_rapt / invalid_grant state — common for human accounts
// after the reauth window expires. The active gcloud account
// (`gcloud auth list`) is the source of truth; for service-account
// runs (e.g. CI), prefer GOOGLE_APPLICATION_CREDENTIALS pointing at a
// SA JSON key file, which the Google SDK picks up automatically and
// makes this helper unnecessary.
type gcloudTokenSource struct{}

// Token shells out to gcloud and parses the resulting access token.
// The lifetime is conservative — gcloud-issued tokens are typically
// 1 hour, but we don't trust the lifetime metadata across versions.
func (gcloudTokenSource) Token() (*oauth2.Token, error) {
	cmd := exec.Command("gcloud", "auth", "print-access-token")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return nil, errors.New("gcloud auth print-access-token returned an empty token")
	}
	return &oauth2.Token{AccessToken: tok, Expiry: time.Now().Add(30 * time.Minute)}, nil
}

// liveAuthOpts returns option.ClientOption(s) suitable for the SDK
// clients used by the inspectors. It tries ADC first; on failure,
// falls back to a `gcloud auth print-access-token` token source so a
// developer with `gcloud auth login` (active account or impersonated
// service account) can run the suite without needing to refresh
// application-default credentials separately.
//
// Critical: ADC's "find" can succeed even when the cached refresh
// token is in invalid_grant / invalid_rapt state — so we MUST issue
// a real token via TokenSource.Token() to confirm before letting
// the SDK use it. Otherwise the per-inspector tests below would
// silently inherit broken ADC even though the regime probe (which
// uses liveHTTPClient with the same check) works fine.
func liveAuthOpts(t *testing.T) []option.ClientOption {
	t.Helper()
	creds, err := google.FindDefaultCredentials(context.Background(), "https://www.googleapis.com/auth/cloud-platform")
	if err == nil && creds != nil {
		if _, terr := creds.TokenSource.Token(); terr == nil {
			return nil
		}
	}
	t.Logf("ADC unavailable or token fetch failed; falling back to `gcloud auth print-access-token`")
	return []option.ClientOption{option.WithTokenSource(gcloudTokenSource{})}
}

// liveHTTPClient returns an http.Client backed by the same auth
// resolution as liveAuthOpts. Used for the direct REST probes in
// TestLive_ComputeV1FilterRegimes (which bypass the SDK to assert
// per-endpoint server-side dialect behavior).
func liveHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	ctx := context.Background()
	if c, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform"); err == nil {
		// Smoke-check by issuing a token; if invalid_grant fires here,
		// fall through to the gcloud fallback below.
		creds, _ := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if creds != nil {
			if _, terr := creds.TokenSource.Token(); terr == nil {
				return c
			}
		}
	}
	t.Logf("ADC unavailable; falling back to `gcloud auth print-access-token` for direct REST probes")
	return oauth2.NewClient(ctx, gcloudTokenSource{})
}

// TestLive_InspectCloudCDN_NoCDNFilter exercises the AggregatedList
// path with no `project` label filter. Must return cleanly (empty
// slice or list of CDN-enabled backend services) with no HTTP 400.
func TestLive_InspectCloudCDN_NoCDNFilter(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)

	got, err := inspectCloudCDN(context.Background(), projectID, "list-backend-services-cdn", "", liveAuthOpts(t)...)
	require.NoError(t, err, "inspectCloudCDN with no project filter must not error against a real project")
	t.Logf("inspectCloudCDN returned %T with content %v", got, got)
}

// TestLive_InspectCloudCDN_WithProjectFilter exercises the
// AggregatedList path with a project-label filter envelope. Per #245
// the handler MUST drop the labels filter server-side (the endpoint
// rejects it with HTTP 400 in both dialects). The call should
// succeed regardless of the filter envelope.
func TestLive_InspectCloudCDN_WithProjectFilter(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)

	label := os.Getenv("LIVE_GCP_PROJECT_LABEL")
	if label == "" {
		// Default to the project ID so the test runs against any
		// configured LIVE_GCP_PROJECT_ID without the operator having
		// to know an existing label value.
		label = projectID
	}

	filters := `{"project":"` + label + `"}`
	got, err := inspectCloudCDN(context.Background(), projectID, "list-backend-services-cdn", filters, liveAuthOpts(t)...)
	require.NoError(t, err,
		"inspectCloudCDN with project-label filter must succeed — HTTP 400 here means the labels filter is being sent server-side (#245 regression)")
	t.Logf("inspectCloudCDN(label=%q) returned %T with content %v", label, got, got)
}

// TestLive_CloudCDNAggregatedListRequest_NoFilter is a defense-in-
// depth pin (mirrors the unit test under live build tags): the
// request constructor never sets req.Filter regardless of inputs.
func TestLive_CloudCDNAggregatedListRequest_NoFilter(t *testing.T) {
	t.Parallel()
	req := cloudCDNAggregatedListRequest("p", `{"project":"io-qtyb4nkwp5n8"}`)
	require.NotNil(t, req)
	assert.Nil(t, req.Filter,
		"req.Filter must be nil — backendServices.aggregatedList rejects labels filters server-side (#245)")
}

// TestLive_ComputeV1FilterRegimes is the canary against future
// Google-side parser changes. It probes each Compute v1 list
// endpoint with both filter dialects and asserts the regime hasn't
// shifted (regime-(a) endpoints stay 400, regime-(b) endpoints stay
// 200, regime-(c) endpoints stay 200 with no filter).
//
// If this test fails after a SDK upgrade or a Google-side change,
// the per-handler dispatch in network.go MUST be re-audited
// before the next release. The bug history (#239, #245) shows that
// silent regressions here ship straight to customers without a live
// gate.
func TestLive_ComputeV1FilterRegimes(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)

	client := liveHTTPClient(t)

	const (
		regimeA  = "rejects all labels filters (HTTP 400)"
		regimeB  = "accepts AIP-160 labels filter (HTTP 200)"
		regimeAB = "accepts both dialects (HTTP 200)"
		regimeC  = "no labels field; only no-filter probe (HTTP 200)"
	)

	type probe struct {
		name        string
		url         string // without ?filter=...
		regime      string
		legacyCode  int    // expected status with the legacy dialect
		aip160Code  int    // expected status with the AIP-160 dialect
		noFilterOK  bool   // expected to return 200 with no filter
	}

	probes := []probe{
		// Regime (a) — both dialects 400
		{"networks.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/networks", regimeA, 400, 400, true},
		{"subnetworks.aggregatedList", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/aggregated/subnetworks", regimeA, 400, 400, true},
		{"backendServices.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/backendServices", regimeA, 400, 400, true},
		{"backendServices.aggregatedList", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/aggregated/backendServices", regimeA, 400, 400, true},
		{"urlMaps.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/urlMaps", regimeA, 400, 400, true},
		{"targetHttpProxies.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/targetHttpProxies", regimeA, 400, 400, true},
		{"targetHttpsProxies.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/targetHttpsProxies", regimeA, 400, 400, true},

		// Regime (b)/(ab) — accept AIP-160 (and most accept legacy too)
		{"globalForwardingRules.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/forwardingRules", regimeAB, 200, 200, true},
		{"securityPolicies.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/securityPolicies", regimeAB, 200, 200, true},
		{"instances.aggregatedList", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/aggregated/instances", regimeAB, 200, 200, true},

		// Regime (c) — no labels field
		{"firewalls.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/firewalls", regimeC, 0, 0, true},
		{"routes.list", "https://compute.googleapis.com/compute/v1/projects/" + projectID + "/global/routes", regimeC, 0, 0, true},
	}

	probeOnce := func(t *testing.T, baseURL, filter string) int {
		t.Helper()
		u := baseURL
		if filter != "" {
			u = baseURL + "?filter=" + url.QueryEscape(filter)
		}
		resp, err := client.Get(u)
		require.NoError(t, err, "GET %s", u)
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("regime: %s", p.regime)

			if p.noFilterOK {
				code := probeOnce(t, p.url, "")
				assert.Equal(t, http.StatusOK, code, "no-filter probe must succeed; sanity-check that the project + auth + endpoint are reachable")
			}

			if p.regime == regimeC {
				return // no labels-filter probes for regime (c)
			}

			legacyCode := probeOnce(t, p.url, `labels.project=io-foo`)
			aip160Code := probeOnce(t, p.url, `labels.project = "io-foo"`)
			t.Logf("legacy → HTTP %d, AIP-160 → HTTP %d", legacyCode, aip160Code)

			assert.Equal(t, p.legacyCode, legacyCode, "legacy dialect regime drift on %s — re-audit the per-handler dispatch in network.go (#245)", p.name)
			assert.Equal(t, p.aip160Code, aip160Code, "AIP-160 dialect regime drift on %s — re-audit the per-handler dispatch in network.go (#245)", p.name)
		})
	}
}

// TestLive_InspectVPC exercises every VPC action against a real
// project. None should error.
func TestLive_InspectVPC(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	ctx := context.Background()

	for _, action := range []string{"list-networks", "list-subnets", "list-firewalls", "list-routes"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			got, err := inspectVPC(ctx, projectID, action, `{"project":"`+projectID+`"}`, liveAuthOpts(t)...)
			require.NoError(t, err, "inspectVPC %s must succeed against a real project (#245)", action)
			t.Logf("%s returned %T", action, got)
		})
	}
}

// TestLive_InspectLoadBalancer exercises every load-balancer action.
func TestLive_InspectLoadBalancer(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	ctx := context.Background()

	for _, action := range []string{
		"list-backend-services", "list-url-maps",
		"list-target-http-proxies", "list-target-https-proxies",
		"list-forwarding-rules",
	} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			got, err := inspectLoadBalancer(ctx, projectID, action, `{"project":"`+projectID+`"}`, liveAuthOpts(t)...)
			require.NoError(t, err, "inspectLoadBalancer %s must succeed (#245)", action)
			t.Logf("%s returned %T", action, got)
		})
	}
}

// TestLive_InspectAPIGateway: list-apis must succeed with the AIP-160
// project filter.
func TestLive_InspectAPIGateway(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	got, err := inspectAPIGateway(context.Background(), projectID, "list-apis", `{"project":"`+projectID+`"}`, liveAuthOpts(t)...)
	require.NoError(t, err, "inspectAPIGateway list-apis must succeed (#245)")
	t.Logf("list-apis returned %T", got)
}

// TestLive_InspectCloudFunctions: gen2 ListFunctions accepts AIP-160
// labels filter (verified live #245). The handler filters server-side.
func TestLive_InspectCloudFunctions(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	got, err := inspectCloudFunctions(context.Background(), projectID, "list-functions", `{"project":"`+projectID+`"}`, liveAuthOpts(t)...)
	require.NoError(t, err, "inspectCloudFunctions list-functions must succeed (#245)")
	t.Logf("list-functions returned %T", got)
}

// TestLive_InspectVertexAI: list-endpoints in the default region.
func TestLive_InspectVertexAI(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	got, err := inspectVertexAI(context.Background(), projectID, "list-endpoints", `{"project":"`+projectID+`"}`, liveAuthOpts(t)...)
	require.NoError(t, err, "inspectVertexAI list-endpoints must succeed (#245)")
	t.Logf("list-endpoints returned %T", got)
}

// TestLive_InspectFirestore_DefaultDB exercises the fallback path —
// when database_name is omitted, the inspector uses (default). Most
// projects don't have a (default) database (the preset uses a named
// DB per #159), so this is allowed to surface a NotFound; the call
// itself must not be silently swallowed.
func TestLive_InspectFirestore_DefaultDB(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	_, err := inspectFirestore(context.Background(), projectID, "list-collections", "", liveAuthOpts(t)...)
	if err != nil {
		t.Logf("inspectFirestore (default) returned (expected on preset-deployed projects): %v", err)
	}
}

// TestLive_InspectFirestore_NamedDB confirms the database_name
// passthrough works against a real preset-deployed Firestore. Set
// LIVE_GCP_FIRESTORE_DB to the database_name output of the deployed
// stack (e.g. io-cc7ndmjcolun-firestore-8a3bfd07).
func TestLive_InspectFirestore_NamedDB(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	dbName := os.Getenv("LIVE_GCP_FIRESTORE_DB")
	if dbName == "" {
		t.Skip("LIVE_GCP_FIRESTORE_DB not set; export the database_name output of a deployed gcp/firestore preset to exercise the named-DB path (#245)")
	}

	filters := `{"database_name":"` + dbName + `"}`
	got, err := inspectFirestore(context.Background(), projectID, "list-collections", filters, liveAuthOpts(t)...)
	require.NoError(t, err, "inspectFirestore with database_name=%q must succeed against a preset-deployed Firestore (#245)", dbName)
	t.Logf("list-collections (db=%s) returned %T: %v", dbName, got, got)
}

// TestLive_InspectIdentityPlatform_TenantsOnUnprovisionedProject
// confirms the structured-error envelope on a project that has the
// API enabled but multi-tenancy not provisioned. By default this
// runs against LIVE_GCP_PROJECT_ID; skip with LIVE_GCP_IDP_HAS_MULTITENANCY=1
// when the project DOES have tenants (in which case it would 200).
func TestLive_InspectIdentityPlatform_TenantsOnUnprovisionedProject(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	if os.Getenv("LIVE_GCP_IDP_HAS_MULTITENANCY") == "1" {
		t.Skip("LIVE_GCP_IDP_HAS_MULTITENANCY=1; project has multi-tenancy provisioned, skipping the structured-error probe")
	}

	_, err := inspectIdentityPlatform(context.Background(), projectID, "list-tenants", "", liveAuthOpts(t)...)
	require.Error(t, err, "list-tenants on a project without provisioned multi-tenancy must error")

	var feErr *observability.GCPFeatureNotEnabledError
	require.True(t, errors.As(err, &feErr),
		"err must be wrapped as GCPFeatureNotEnabledError so reliable's panel renderer can errors.As it (#245); got %T (%v)", err, err)
	assert.Equal(t, "identity_platform_multitenancy", feErr.Feature)
	assert.Equal(t, projectID, feErr.ProjectID)
}

// TestLive_InspectIdentityPlatform_ListProviders — list-providers
// works on every project with the Identity Toolkit API enabled, so
// it's a baseline sanity check that the project + creds are right.
func TestLive_InspectIdentityPlatform_ListProviders(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	got, err := inspectIdentityPlatform(context.Background(), projectID, "list-providers", "", liveAuthOpts(t)...)
	require.NoError(t, err)
	t.Logf("list-providers returned %T: %v", got, got)
}
