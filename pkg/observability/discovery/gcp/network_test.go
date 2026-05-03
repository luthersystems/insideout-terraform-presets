package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	computeapi "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// fakeComputeAPIREST stands in for the legacy
// google.golang.org/api/compute/v1 endpoint shared by VPC,
// LoadBalancer, and Cloud Armor inspectors. The endpoint paths look
// like /compute/v1/projects/<project>/global/networks etc.
func fakeComputeAPIREST(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []option.ClientOption) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, []option.ClientOption{
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	}
}

const networksListResponse = `{
  "kind": "compute#networkList",
  "items": [
    {"name":"net-a","autoCreateSubnetworks":false},
    {"name":"net-b","autoCreateSubnetworks":true}
  ]
}`

func TestInspectVPC_ListNetworks(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/networks") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(networksListResponse))
	})
	defer srv.Close()

	got, err := inspectVPC(context.Background(), "demo-proj", "list-networks", "", opts...)
	require.NoError(t, err)

	nets, ok := got.([]*computeapi.Network)
	require.True(t, ok, "expected []*Network, got %T", got)
	assert.Len(t, nets, 2)
	// No project filter passed; filter param should be empty.
	assert.Empty(t, capturedFilter)
}

// TestInspectVPC_ListNetworks_NoServerSideLabelsFilter pins #245:
// networks.list rejects ALL labels filters with HTTP 400 ("Invalid
// list filter expression") because Network has no `labels` field on
// the v1 schema. The handler MUST send no `filter` query parameter
// even when the caller passes a project filter; project-scoping
// happens at the GCP project boundary, not via per-resource labels.
//
// If a future refactor re-introduces gcpAIP160LabelFilter or
// gcpLegacyLabelFilter on this call site, this test fails. Reverses
// the previous (broken) #239 broadening (commit 8465e59).
func TestInspectVPC_ListNetworks_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"net-a"}]}`))
	})
	defer srv.Close()

	_, err := inspectVPC(context.Background(), "demo-proj", "list-networks",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Empty(t, capturedFilter,
		"networks.list rejects labels filters server-side; handler must send no filter param (#245)")
}

func TestInspectVPC_ListFirewallsUnfiltered(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/firewalls") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"allow-ssh"}]}`))
	})
	defer srv.Close()

	// Even with a project filter, firewalls have no labels field on
	// the GCE v1 schema — the handler must NOT pass the filter.
	got, err := inspectVPC(context.Background(), "demo-proj", "list-firewalls",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	fws, ok := got.([]*computeapi.Firewall)
	require.True(t, ok)
	assert.Len(t, fws, 1)
	assert.Empty(t, capturedFilter, "firewalls have no labels field — filter must NOT be set")
}

func TestInspectVPC_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectVPC(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported VPC action")
}

// TestInspectLoadBalancer_ListBackendServices_NoServerSideLabelsFilter
// pins #245: backendServices.list rejects ALL labels filters with
// HTTP 400 because BackendService has no `labels` field on the v1
// schema. Same regime applies to UrlMaps, TargetHttp(s)Proxies — all
// covered by the regime-(a) live-probe table in network.go. Handler
// MUST not send a filter query param.
func TestInspectLoadBalancer_ListBackendServices_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"bs-a"}]}`))
	})
	defer srv.Close()

	_, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-backend-services",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Empty(t, capturedFilter,
		"backendServices.list rejects labels filters server-side; handler must send no filter param (#245)")
}

// TestInspectLoadBalancer_ListUrlMaps_NoServerSideLabelsFilter — same
// regime as backend-services per the network.go header comment.
func TestInspectLoadBalancer_ListUrlMaps_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"um-a"}]}`))
	})
	defer srv.Close()

	_, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-url-maps",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Empty(t, capturedFilter,
		"urlMaps.list rejects labels filters server-side; handler must send no filter param (#245)")
}

// TestInspectLoadBalancer_ListTargetHttp{,s}Proxies_NoServerSideLabelsFilter
// — same regime.
func TestInspectLoadBalancer_ListTargetHttpProxies_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"thp-a"}]}`))
	})
	defer srv.Close()

	_, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-target-http-proxies",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Empty(t, capturedFilter)
}

func TestInspectLoadBalancer_ListTargetHttpsProxies_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"thsp-a"}]}`))
	})
	defer srv.Close()

	_, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-target-https-proxies",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Empty(t, capturedFilter)
}

// TestInspectLoadBalancer_ListForwardingRules_AppliesAIP160Filter
// pins #245 regime (b): globalForwardingRules.list accepts the
// AIP-160 server-side label filter. ForwardingRule carries `labels`
// on the v1 schema, so the handler keeps the original behavior.
func TestInspectLoadBalancer_ListForwardingRules_AppliesAIP160Filter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	defer srv.Close()

	_, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-forwarding-rules",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Equal(t, `labels.project = "io-foo"`, capturedFilter,
		"globalForwardingRules.list accepts AIP-160; handler must keep server-side filter (#245)")
}

func TestInspectLoadBalancer_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectLoadBalancer(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Load Balancer action")
}

func TestInspectCloudArmor_ListPolicies_AppliesFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"policy-a"}]}`))
	})
	defer srv.Close()

	_, err := inspectCloudArmor(context.Background(), "demo-proj", "list-policies",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Equal(t, `labels.project = "io-foo"`, capturedFilter)
}

func TestInspectCloudArmor_DescribePolicy_MissingFilter(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudArmor(context.Background(), "demo-proj", "describe-policy", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "policy in filters")
}

func TestInspectCloudArmor_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudArmor(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud Armor action")
}

func TestInspectCloudCDN_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudCDN(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud CDN action")
}

// TestCloudCDNAggregatedListRequest_NoServerSideLabelsFilter pins
// the corrected #245 contract: backendServices.aggregatedList rejects
// labels filters in BOTH dialects (legacy AND AIP-160) with HTTP 400
// "Invalid list filter expression". BackendService has no `labels`
// field on the v1 schema, so server-side label filtering is
// impossible regardless of dialect. The constructor MUST NOT set
// req.Filter.
//
// This reverses the (broken) #239 unit test
// `TestCloudCDNAggregatedListRequest_AIP160DialectForProjectFilter`
// — that test pinned a wire format the live server actually rejects.
// Without a live integration test, the bug shipped to v0.9.0; the
// new TestLive_ComputeV1FilterRegimes integration test in
// live_integration_test.go closes that loop.
func TestCloudCDNAggregatedListRequest_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing project key", `{"region":"us-central1"}`},
		{"empty project value", `{"project":""}`},
		{"populated project", `{"project":"io-qtyb4nkwp5n8"}`},
		{"populated project + extra", `{"project":"io-foo","region":"us-central1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := cloudCDNAggregatedListRequest("demo-proj", tc.filters)
			require.NotNil(t, req)
			assert.Equal(t, "demo-proj", req.GetProject())
			assert.Nil(t, req.Filter,
				"req.Filter must be nil — backendServices.aggregatedList rejects labels filters server-side regardless of dialect (#245)")
		})
	}
}

func TestInspectAPIGateway_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectAPIGateway(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported API Gateway action")
}
