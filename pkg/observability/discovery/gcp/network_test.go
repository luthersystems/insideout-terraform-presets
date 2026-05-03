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

func TestInspectVPC_ListNetworks_AppliesProjectFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	defer srv.Close()

	_, err := inspectVPC(context.Background(), "demo-proj", "list-networks",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Equal(t, "labels.project=io-foo", capturedFilter)
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

func TestInspectLoadBalancer_ListBackendServices_AppliesFilter(t *testing.T) {
	t.Parallel()
	var capturedFilter string
	srv, opts := fakeComputeAPIREST(t, func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	defer srv.Close()

	_, err := inspectLoadBalancer(context.Background(), "demo-proj", "list-backend-services",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)
	assert.Equal(t, "labels.project=io-foo", capturedFilter)
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
	assert.Equal(t, "labels.project=io-foo", capturedFilter)
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

// TestCloudCDNAggregatedListRequest_AIP160DialectForProjectFilter pins
// the dialect choice from #239: backendServices.aggregatedList rejects
// the GCE legacy filter form (`labels.project=<value>`) with HTTP 400
// "Invalid list filter expression" and requires the AIP-160 form
// (`labels.project = "<value>"`). This is the same Compute v1 REST API
// as VPC / LoadBalancer / CloudArmor, but accessed via the newer
// cloud.google.com/go/compute/apiv1 client which enforces AIP-160.
//
// If a future refactor flips this back to gcpLegacyLabelFilter (or
// changes the helper's output shape), this test fails. Reproduces the
// staging session sess_v2_qtyB4nkwp5N8 / project io-qtyb4nkwp5n8 bug.
func TestCloudCDNAggregatedListRequest_AIP160DialectForProjectFilter(t *testing.T) {
	t.Parallel()
	req := cloudCDNAggregatedListRequest("demo-proj", `{"project":"io-qtyb4nkwp5n8"}`)

	require.NotNil(t, req, "request must always be returned")
	assert.Equal(t, "demo-proj", req.GetProject(), "Project must be set on the AggregatedList request")
	require.NotNil(t, req.Filter, "non-empty project filter must populate req.Filter")
	assert.Equal(t, `labels.project = "io-qtyb4nkwp5n8"`, *req.Filter,
		"filter must use AIP-160 dialect (spaces around =, quoted value); the GCE legacy form is rejected by backendServices.aggregatedList with HTTP 400 — see #239")
}

// TestCloudCDNAggregatedListRequest_NoFilterWhenProjectMissing — no
// filter must be set when the caller doesn't supply a project; the SDK
// treats nil as "no filter" / match all.
func TestCloudCDNAggregatedListRequest_NoFilterWhenProjectMissing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing project key", `{"region":"us-central1"}`},
		{"empty project value", `{"project":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := cloudCDNAggregatedListRequest("demo-proj", tc.filters)
			require.NotNil(t, req)
			assert.Equal(t, "demo-proj", req.GetProject())
			assert.Nil(t, req.Filter, "no project filter → req.Filter must be nil so backendServices.aggregatedList returns everything in the project")
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
