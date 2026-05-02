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

func TestInspectAPIGateway_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectAPIGateway(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported API Gateway action")
}
