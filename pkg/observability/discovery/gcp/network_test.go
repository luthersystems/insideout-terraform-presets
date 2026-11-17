package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/apigateway/apiv1/apigatewaypb"
	"cloud.google.com/go/compute/apiv1/computepb"
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

// firewallsListResponse: net-a has one rule open to the internet (plus
// rules that must NOT count: internal source, disabled, deny, egress);
// net-b has none.
const firewallsListResponse = `{
  "kind": "compute#firewallList",
  "items": [
    {"name":"allow-http","network":"https://www.googleapis.com/compute/v1/projects/demo-proj/global/networks/net-a","direction":"INGRESS","sourceRanges":["0.0.0.0/0"],"allowed":[{"IPProtocol":"tcp","ports":["80"]}]},
    {"name":"allow-internal","network":"projects/demo-proj/global/networks/net-b","direction":"INGRESS","sourceRanges":["10.0.0.0/8"],"allowed":[{"IPProtocol":"all"}]},
    {"name":"disabled-open","network":"projects/demo-proj/global/networks/net-b","sourceRanges":["0.0.0.0/0"],"allowed":[{"IPProtocol":"all"}],"disabled":true},
    {"name":"deny-all","network":"projects/demo-proj/global/networks/net-b","direction":"INGRESS","sourceRanges":["0.0.0.0/0"],"denied":[{"IPProtocol":"all"}]},
    {"name":"egress-open","network":"projects/demo-proj/global/networks/net-b","direction":"EGRESS","destinationRanges":["0.0.0.0/0"],"allowed":[{"IPProtocol":"all"}]}
  ]
}`

func TestInspectVPC_ListNetworks(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/global/networks"):
			capturedFilter = r.URL.Query().Get("filter")
			_, _ = w.Write([]byte(networksListResponse))
		case strings.HasSuffix(r.URL.Path, "/global/firewalls"):
			_, _ = w.Write([]byte(firewallsListResponse))
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
		}
	})
	defer srv.Close()

	got, err := inspectVPC(context.Background(), "demo-proj", "list-networks", "", opts...)
	require.NoError(t, err)

	nets, ok := got.([]map[string]any)
	require.True(t, ok, "expected []map[string]any, got %T", got)
	require.Len(t, nets, 2)
	// No project filter passed; filter param should be empty.
	assert.Empty(t, capturedFilter)

	// Network fields keep the Compute JSON shape the extractor reads.
	assert.Equal(t, "net-b", nets[1]["name"])
	assert.Equal(t, true, nets[1]["autoCreateSubnetworks"])
	// The firewall join (#928): only the enabled INGRESS allow rule from
	// 0.0.0.0/0 counts; net-b is explicitly private ([] not null).
	b, err := json.Marshal(nets[0]["publicIngressFirewalls"])
	require.NoError(t, err)
	assert.Equal(t, `["allow-http"]`, string(b))
	b, err = json.Marshal(nets[1]["publicIngressFirewalls"])
	require.NoError(t, err)
	assert.Equal(t, `[]`, string(b))
}

// TestInspectVPC_ListNetworks_FirewallFailureIsNonFatal: a failed
// firewalls.list must not fail list-networks; the networks come back
// without publicIngressFirewalls so the extractor skips the
// classification instead of calling them private (#928).
func TestInspectVPC_ListNetworks_FirewallFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/global/firewalls") {
			http.Error(w, "permission denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(networksListResponse))
	})
	defer srv.Close()

	got, err := inspectVPC(context.Background(), "demo-proj", "list-networks", "", opts...)
	require.NoError(t, err)
	nets, ok := got.([]map[string]any)
	require.True(t, ok)
	require.Len(t, nets, 2)
	for _, n := range nets {
		assert.NotContains(t, n, "publicIngressFirewalls")
	}
}

func TestInspectVPC_ListNetworks_Empty_MarshalsAsArray(t *testing.T) {
	t.Parallel()
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	defer srv.Close()

	got, err := inspectVPC(context.Background(), "demo-proj", "list-networks", "", opts...)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b), "empty list-networks must marshal as [] not null (#255)")
}

// TestInspectLoadBalancer_ListUrlMaps_JoinsBackendsAndProxies pins the
// #928 join: the default backend service's protocol and scheme, and the
// SSL policies of the HTTPS proxies that point at each map. Both joins
// read one project-wide list each, with no labels filter (regime (a)).
func TestInspectLoadBalancer_ListUrlMaps_JoinsBackendsAndProxies(t *testing.T) {
	t.Parallel()
	var filters []string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		filters = append(filters, r.URL.Query().Get("filter"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/global/urlMaps"):
			_, _ = w.Write([]byte(`{"items":[
				{"name":"um-a","defaultService":"https://www.googleapis.com/compute/v1/projects/demo-proj/global/backendServices/bs-a"},
				{"name":"um-b","defaultService":"https://www.googleapis.com/compute/v1/projects/demo-proj/global/backendBuckets/bb-b"}]}`))
		case strings.HasSuffix(r.URL.Path, "/global/backendServices"):
			_, _ = w.Write([]byte(`{"items":[
				{"name":"bs-a","protocol":"HTTPS","loadBalancingScheme":"EXTERNAL_MANAGED"},
				{"name":"bb-b","protocol":"HTTP","loadBalancingScheme":"EXTERNAL"}]}`))
		case strings.HasSuffix(r.URL.Path, "/global/targetHttpsProxies"):
			_, _ = w.Write([]byte(`{"items":[
				{"name":"px-a","urlMap":"https://www.googleapis.com/compute/v1/projects/demo-proj/global/urlMaps/um-a","sslPolicy":"https://www.googleapis.com/compute/v1/projects/demo-proj/global/sslPolicies/modern"}]}`))
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
		}
	})
	defer srv.Close()

	got, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-url-maps", `{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	maps, ok := got.([]map[string]any)
	require.True(t, ok, "expected []map[string]any, got %T", got)
	require.Len(t, maps, 2)

	assert.Equal(t, "um-a", maps[0]["name"])
	assert.Equal(t, "HTTPS", maps[0]["defaultServiceProtocol"])
	assert.Equal(t, "EXTERNAL_MANAGED", maps[0]["defaultServiceLoadBalancingScheme"])
	assert.Equal(t, 1, maps[0]["httpsProxyCount"])
	assert.Equal(t, []string{"https://www.googleapis.com/compute/v1/projects/demo-proj/global/sslPolicies/modern"}, maps[0]["sslPolicies"])

	// um-b's default is a backend BUCKET: a backend service that happens
	// to share its name must not be joined. No HTTPS proxy → count 0, [].
	assert.NotContains(t, maps[1], "defaultServiceProtocol")
	assert.Equal(t, 0, maps[1]["httpsProxyCount"])
	b, err := json.Marshal(maps[1]["sslPolicies"])
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))

	for _, f := range filters {
		assert.Empty(t, f, "url maps, backend services and HTTPS proxies reject labels filters (#245)")
	}
}

// TestInspectLoadBalancer_ListUrlMaps_JoinFailuresAreNonFatal: when the
// backend-services and proxies calls fail, the URL maps still come back,
// without the keys those calls would have filled in (#928).
func TestInspectLoadBalancer_ListUrlMaps_JoinFailuresAreNonFatal(t *testing.T) {
	t.Parallel()
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/global/urlMaps") {
			http.Error(w, "permission denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"um-a","defaultService":"projects/demo-proj/global/backendServices/bs-a"}]}`))
	})
	defer srv.Close()

	got, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-url-maps", "", opts...)
	require.NoError(t, err)
	maps, ok := got.([]map[string]any)
	require.True(t, ok)
	require.Len(t, maps, 1)
	assert.Equal(t, "um-a", maps[0]["name"])
	for _, k := range []string{"defaultServiceProtocol", "defaultServiceLoadBalancingScheme", "httpsProxyCount", "sslPolicies"} {
		assert.NotContains(t, maps[0], k)
	}
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
//
// The fake handler asserts the URL path so a future switch-case
// crossing (e.g. list-backend-services accidentally calling
// UrlMaps.List) is caught by the path guard, not silently passed as
// "filter param empty" (qa-professor §P2.4).
func TestInspectLoadBalancer_ListBackendServices_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/backendServices") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
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
		if !strings.Contains(r.URL.Path, "/urlMaps") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
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
		// Note the trailing-slash-resistant guard: targetHttpProxies
		// has a longer cousin (targetHttpsProxies) — match on the
		// exact path segment.
		if !strings.Contains(r.URL.Path, "/targetHttpProxies") || strings.Contains(r.URL.Path, "/targetHttpsProxies") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
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
		if !strings.Contains(r.URL.Path, "/targetHttpsProxies") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
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
		if !strings.Contains(r.URL.Path, "/forwardingRules") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
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
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(r.URL.Path, "/global/securityPolicies") {
			// describe fan-out for the rule-less policy below.
			_, _ = w.Write([]byte(`{"name":"policy-a"}`))
			return
		}
		capturedFilter = r.URL.Query().Get("filter")
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
//
// One row is enough — the constructor's signature ignores `filters`
// (`_ string`), so iterating over many rows would be tautological
// (qa-professor §6). The behavioral pin against `inspectCloudCDN` +
// fakeComputeAPIREST in TestInspectCloudCDN_NoLabelsFilter exercises
// the live wire path.
func TestCloudCDNAggregatedListRequest_NoServerSideLabelsFilter(t *testing.T) {
	t.Parallel()
	req := cloudCDNAggregatedListRequest("demo-proj", `{"project":"io-qtyb4nkwp5n8"}`)
	require.NotNil(t, req)
	assert.Equal(t, "demo-proj", req.GetProject())
	assert.Nil(t, req.Filter,
		"req.Filter must be nil — backendServices.aggregatedList rejects labels filters server-side regardless of dialect (#245)")
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

// Empty-state pins per #256 for the network-plane sites that route
// through drainIterator / drainAggregatedIterator.

func TestInspectAPIGateway_ListAPIs_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*apigatewaypb.Api]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty API Gateway list-apis must marshal as [] not null (#256)")
}

// TestInspectVPC_ListSubnets_NoMatches_EmptySlice exercises the
// post-drain path where AggregatedList returns no items per region.
// inspectVPC list-subnets walks the SDK's resp.Items map directly
// (not drainAggregatedIterator), so this httptest fake exercises the
// full inspector path.
func TestInspectVPC_ListSubnets_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/aggregated/subnetworks") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Empty AggregatedList — items map omitted entirely so the
		// per-region loop never enters.
		_, _ = w.Write([]byte(`{"kind":"compute#subnetworkAggregatedList","items":{}}`))
	})
	defer srv.Close()

	got, err := inspectVPC(context.Background(), "demo-proj", "list-subnets", "", opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")

	subnets, ok := got.([]*computeapi.Subnetwork)
	require.True(t, ok, "expected []*computeapi.Subnetwork, got %T", got)
	assert.Empty(t, subnets)

	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty VPC list-subnets must marshal as [] not null (#256)")
}

func TestInspectCloudCDN_ListBackendServices_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	// inspectCloudCDN routes through drainAggregatedIterator with the
	// EnableCDN predicate. Pin the empty-iterator path; the EnableCDN
	// closure is exercised by the existing happy-path tests.
	got, err := drainAggregatedIterator(
		&emptyIterator[any]{},
		func(any) []*computepb.BackendService { return nil },
		func(*computepb.BackendService) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cloud CDN list-backend-services-cdn must marshal as [] not null (#256)")
}

// TestInspectCloudArmor_ListPolicies_DescribesRulelessPolicies pins the
// #928 fan-out: a policy listed with its rules is returned as listed; one
// listed without rules is described with get; a failed get keeps the
// listed policy and does not fail the call.
func TestInspectCloudArmor_ListPolicies_DescribesRulelessPolicies(t *testing.T) {
	t.Parallel()
	var gets []string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/global/securityPolicies"):
			_, _ = w.Write([]byte(`{"items":[
				{"name":"listed","rules":[{"priority":2147483647,"action":"allow"}]},
				{"name":"bare"},
				{"name":"broken"}]}`))
		case strings.HasSuffix(r.URL.Path, "/securityPolicies/bare"):
			gets = append(gets, "bare")
			_, _ = w.Write([]byte(`{"name":"bare","rules":[{"priority":1000,"action":"allow"},{"priority":2147483647,"action":"deny(403)"}]}`))
		case strings.HasSuffix(r.URL.Path, "/securityPolicies/broken"):
			gets = append(gets, "broken")
			http.Error(w, "boom", http.StatusForbidden)
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
		}
	})
	defer srv.Close()

	got, err := inspectCloudArmor(context.Background(), "demo-proj", "list-policies", "", opts...)
	require.NoError(t, err)
	policies, ok := got.([]*computeapi.SecurityPolicy)
	require.True(t, ok, "expected []*SecurityPolicy, got %T", got)
	require.Len(t, policies, 3)
	assert.ElementsMatch(t, []string{"bare", "broken"}, gets, "only rule-less policies are described")
	assert.Len(t, policies[0].Rules, 1)
	assert.Len(t, policies[1].Rules, 2)
	assert.Equal(t, "deny(403)", policies[1].Rules[1].Action)
	assert.Equal(t, "broken", policies[2].Name)
	assert.Empty(t, policies[2].Rules)
}

func TestWithPolicyRules_CapAndEmpty(t *testing.T) {
	t.Parallel()
	policies := make([]*computeapi.SecurityPolicy, cloudArmorDescribeCap+3)
	for i := range policies {
		policies[i] = &computeapi.SecurityPolicy{Name: fmt.Sprintf("p%d", i)}
	}
	calls := 0
	got := withPolicyRules(context.Background(), policies, func(_ context.Context, name string) (*computeapi.SecurityPolicy, error) {
		calls++
		return &computeapi.SecurityPolicy{Name: name, Rules: []*computeapi.SecurityPolicyRule{{Priority: 2147483647, Action: "allow"}}}, nil
	})
	assert.Equal(t, cloudArmorDescribeCap, calls, "describe fan-out is bounded")
	require.Len(t, got, len(policies), "policies past the cap are still returned")
	assert.Len(t, got[cloudArmorDescribeCap-1].Rules, 1)
	assert.Empty(t, got[cloudArmorDescribeCap].Rules)

	empty := withPolicyRules(context.Background(), nil, nil)
	b, err := json.Marshal(empty)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b), "empty list-policies must marshal as [] not null (#255)")
}

func TestIsPublicIngressFirewall(t *testing.T) {
	t.Parallel()
	allow := []*computeapi.FirewallAllowed{{IPProtocol: "tcp"}}
	cases := []struct {
		name string
		fw   *computeapi.Firewall
		want bool
	}{
		{"open ipv4", &computeapi.Firewall{SourceRanges: []string{"0.0.0.0/0"}, Allowed: allow}, true},
		{"open ipv6", &computeapi.Firewall{Direction: "INGRESS", SourceRanges: []string{"::/0"}, Allowed: allow}, true},
		{"internal only", &computeapi.Firewall{SourceRanges: []string{"10.0.0.0/8"}, Allowed: allow}, false},
		{"disabled", &computeapi.Firewall{SourceRanges: []string{"0.0.0.0/0"}, Allowed: allow, Disabled: true}, false},
		{"deny rule", &computeapi.Firewall{SourceRanges: []string{"0.0.0.0/0"}, Denied: []*computeapi.FirewallDenied{{IPProtocol: "all"}}}, false},
		{"egress", &computeapi.Firewall{Direction: "EGRESS", SourceRanges: []string{"0.0.0.0/0"}, Allowed: allow}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isPublicIngressFirewall(tc.fw), tc.name)
	}
}
