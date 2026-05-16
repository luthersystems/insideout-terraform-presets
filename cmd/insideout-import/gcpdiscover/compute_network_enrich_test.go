package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	computev1 "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestMapComputeNetwork_Minimal pins the smallest non-trivial
// mapping. Verifies the always-emitted defaults
// (auto_create_subnetworks, enable_ula_internal_ipv6,
// delete_default_routes_on_create=false) and the skipped
// computed-only fields.
func TestMapComputeNetwork_Minimal(t *testing.T) {
	t.Parallel()
	src := &computev1.Network{
		Name: "default",
	}
	got := mapComputeNetwork(src, "my-project")

	require.NotNil(t, got.Name)
	assert.Equal(t, "default", *got.Name.Literal)
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)
	require.NotNil(t, got.AutoCreateSubnetworks)
	assert.False(t, *got.AutoCreateSubnetworks.Literal)
	require.NotNil(t, got.EnableUlaInternalIPV6)
	assert.False(t, *got.EnableUlaInternalIPV6.Literal)
	require.NotNil(t, got.DeleteDefaultRoutesOnCreate)
	assert.False(t, *got.DeleteDefaultRoutesOnCreate.Literal,
		"TF-only sentinel must emit as false to match the schema default (decision #34)")

	// Computed-only fields must not be populated.
	assert.Nil(t, got.ID, "id is computed-only")
	assert.Nil(t, got.GatewayIPV4, "gateway_ipv4 is computed-only")
	assert.Nil(t, got.NumericID, "numeric_id is computed-only")
	assert.Nil(t, got.SelfLink, "self_link is computed-only")
	assert.Nil(t, got.Timeouts, "timeouts is a TF-only sentinel")

	// Optional fields untouched.
	assert.Nil(t, got.Description)
	assert.Nil(t, got.InternalIPV6Range)
	assert.Nil(t, got.Mtu)
	assert.Nil(t, got.RoutingMode)
	assert.Nil(t, got.NetworkFirewallPolicyEnforcementOrder)
}

// TestMapComputeNetwork_FullyPopulated covers the routing_mode
// indirection through RoutingConfig + the int64-to-float64 mtu cast.
func TestMapComputeNetwork_FullyPopulated(t *testing.T) {
	t.Parallel()
	src := &computev1.Network{
		Name:                                  "vpc-prod",
		Description:                           "Production VPC",
		AutoCreateSubnetworks:                 true,
		EnableUlaInternalIpv6:                 true,
		InternalIpv6Range:                     "fd20:abcd:1234::/48",
		Mtu:                                   1500,
		NetworkFirewallPolicyEnforcementOrder: "AFTER_CLASSIC_FIREWALL",
		RoutingConfig: &computev1.NetworkRoutingConfig{
			RoutingMode: "GLOBAL",
		},
	}
	got := mapComputeNetwork(src, "my-project")

	require.NotNil(t, got.Description)
	assert.Equal(t, "Production VPC", *got.Description.Literal)
	require.NotNil(t, got.AutoCreateSubnetworks)
	assert.True(t, *got.AutoCreateSubnetworks.Literal)
	require.NotNil(t, got.EnableUlaInternalIPV6)
	assert.True(t, *got.EnableUlaInternalIPV6.Literal)
	require.NotNil(t, got.InternalIPV6Range)
	assert.Equal(t, "fd20:abcd:1234::/48", *got.InternalIPV6Range.Literal)
	require.NotNil(t, got.Mtu)
	assert.Equal(t, float64(1500), *got.Mtu.Literal,
		"engine must cast int64 API mtu to *Value[float64] (the typed schema's chosen shape)")
	require.NotNil(t, got.NetworkFirewallPolicyEnforcementOrder)
	assert.Equal(t, "AFTER_CLASSIC_FIREWALL", *got.NetworkFirewallPolicyEnforcementOrder.Literal)
	require.NotNil(t, got.RoutingMode)
	assert.Equal(t, "GLOBAL", *got.RoutingMode.Literal,
		"routing_mode must come from RoutingConfig.RoutingMode (override-only path)")
}

// TestMapComputeNetwork_RoutingModeNilGuards covers both nil-guard
// branches on the routing_mode override.
func TestMapComputeNetwork_RoutingModeNilGuards(t *testing.T) {
	t.Parallel()
	t.Run("nil_routing_config", func(t *testing.T) {
		got := mapComputeNetwork(&computev1.Network{Name: "n"}, "p")
		assert.Nil(t, got.RoutingMode, "nil RoutingConfig must leave routing_mode unset")
	})
	t.Run("empty_routing_mode_string", func(t *testing.T) {
		got := mapComputeNetwork(&computev1.Network{
			Name:          "n",
			RoutingConfig: &computev1.NetworkRoutingConfig{RoutingMode: ""},
		}, "p")
		assert.Nil(t, got.RoutingMode, "empty RoutingMode must not emit")
	})
}

// Enricher contract tests.
func TestComputeNetworkEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeNetworkEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_compute_network", ImportID: "projects/p/global/networks/default",
			Address: "google_compute_network.default",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

// TestComputeNetworkEnrich_ProjectIDRequired pins the compute-API
// quirk: Networks.Get is positional (project, network), not a single
// fully-qualified name, so ProjectID on EnrichClients is structurally
// required even when Identity.ImportID encodes the project.
func TestComputeNetworkEnrich_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := computeNetworkEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _ string) (*computev1.Network, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_compute_network", ImportID: "projects/p/global/networks/default",
			Address: "google_compute_network.default",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EnrichClients.ProjectID required")
}

func TestComputeNetworkEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := computeNetworkEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _ string) (*computev1.Network, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_compute_network"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive network name")
}

func TestComputeNetworkEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("404 not found")
	var gotProject, gotName string
	e := computeNetworkEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, project, name string) (*computev1.Network, error) {
			gotProject, gotName = project, name
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_network",
			ImportID: "projects/p/global/networks/default",
			Address:  "google_compute_network.default",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "my-project/default")
	assert.Equal(t, "my-project", gotProject, "fetch must receive ProjectID from EnrichClients, not from Identity")
	assert.Equal(t, "default", gotName)
}

func TestComputeNetworkEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	net := &computev1.Network{
		Name:                  "vpc-prod",
		Description:           "Production VPC",
		AutoCreateSubnetworks: true,
		Mtu:                   1500,
		RoutingConfig:         &computev1.NetworkRoutingConfig{RoutingMode: "GLOBAL"},
	}
	e := computeNetworkEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, project, name string) (*computev1.Network, error) {
			assert.Equal(t, "my-project", project)
			assert.Equal(t, "vpc-prod", name)
			return net, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_network",
			ImportID: "projects/my-project/global/networks/vpc-prod",
			Address:  "google_compute_network.vpc_prod",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}))

	decoded, err := generated.UnmarshalAttrs("google_compute_network", ir.Attrs)
	require.NoError(t, err)
	gn, ok := decoded.(*generated.GoogleComputeNetwork)
	require.True(t, ok)
	require.NotNil(t, gn.Name)
	assert.Equal(t, "vpc-prod", *gn.Name.Literal)
	require.NotNil(t, gn.RoutingMode)
	assert.Equal(t, "GLOBAL", *gn.RoutingMode.Literal)
	require.NotNil(t, gn.Mtu)
	assert.Equal(t, float64(1500), *gn.Mtu.Literal)
}

// TestComputeNetworkEnrich_RoundTripThroughEmitImportedTF — decision-
// #34 contract. Critical assertions: routing_mode appears as a
// top-level scalar (not nested under routing_config), and the
// computed-only fields (gateway_ipv4, numeric_id, self_link) are
// absent from the emitted HCL.
func TestComputeNetworkEnrich_RoundTripThroughEmitImportedTF(t *testing.T) {
	t.Parallel()
	net := &computev1.Network{
		Name:                  "vpc-prod",
		Description:           "Production VPC",
		AutoCreateSubnetworks: true,
		EnableUlaInternalIpv6: false,
		Mtu:                   1460,
		RoutingConfig:         &computev1.NetworkRoutingConfig{RoutingMode: "REGIONAL"},
	}
	typed := mapComputeNetwork(net, "my-project")
	raw, err := json.Marshal(typed)
	require.NoError(t, err)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "gcp", Type: "google_compute_network",
			Address:  "google_compute_network.vpc_prod",
			ImportID: "projects/my-project/global/networks/vpc-prod",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: raw,
	}
	out, used := composer.EmitImportedTF("gcp", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.NotNil(t, out)
	require.True(t, used["gcp"])
	s := string(out)

	assert.Contains(t, s, `resource "google_compute_network" "vpc_prod"`)
	assert.Regexp(t, `(?m)^\s*name\s+=\s+"vpc-prod"`, s)
	assert.Regexp(t, `(?m)^\s*project\s+=\s+"my-project"`, s)
	assert.Regexp(t, `(?m)^\s*auto_create_subnetworks\s+=\s+true`, s)
	assert.Regexp(t, `(?m)^\s*description\s+=\s+"Production VPC"`, s)
	assert.Regexp(t, `(?m)^\s*mtu\s+=\s+1460`, s)
	assert.Regexp(t, `(?m)^\s*routing_mode\s+=\s+"REGIONAL"`, s,
		"routing_mode must emit as a top-level scalar (not nested under routing_config)")
	assert.Regexp(t, `(?m)^\s*delete_default_routes_on_create\s+=\s+false`, s,
		"TF-only sentinel must emit explicitly to match schema default")

	// Computed-only fields must NOT appear in emitted HCL.
	for _, computed := range []string{"gateway_ipv4", "numeric_id", "self_link"} {
		assert.NotContains(t, s, computed, "computed-only field %q must not appear", computed)
	}
}

func TestComputeNetworkRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_compute_network"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_compute_network", enr.ResourceType())
	_, isByID := enr.(ByIDEnricher)
	assert.True(t, isByID, "compute_network enricher must satisfy ByIDEnricher (#571)")
}

// ---------------------------------------------------------------
// ByIDEnricher tests (issue #571).
// ---------------------------------------------------------------

func TestComputeNetworkEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newComputeNetworkEnricher().(*computeNetworkEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestComputeNetworkEnrichByID_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeNetworkEnricher().(*computeNetworkEnricher)
	id := &imported.ResourceIdentity{
		Type:     "google_compute_network",
		ImportID: "projects/p/global/networks/vpc-prod",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Nil(t, raw)
}

func TestComputeNetworkEnrichByID_ProjectIDRequired(t *testing.T) {
	t.Parallel()
	e := newComputeNetworkEnricher().(*computeNetworkEnricher)
	id := &imported.ResourceIdentity{
		Type:     "google_compute_network",
		ImportID: "projects/p/global/networks/vpc-prod",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: ""})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "ProjectID required")
}

func TestComputeNetworkEnrichByID_NotFound(t *testing.T) {
	t.Parallel()
	e := computeNetworkEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _ string) (*computev1.Network, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{
		Type:     "google_compute_network",
		ImportID: "projects/p/global/networks/vpc-prod",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestComputeNetworkEnrichByID_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := computeNetworkEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _ string) (*computev1.Network, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{
		Type:     "google_compute_network",
		ImportID: "projects/p/global/networks/vpc-prod",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
	assert.Equal(t, http.StatusForbidden, gerr.Code)
	assert.Nil(t, raw)
}

func TestComputeNetworkEnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	net := &computev1.Network{
		Name:                  "vpc-prod",
		AutoCreateSubnetworks: true,
		Description:           "Production VPC",
		Mtu:                   1460,
		RoutingConfig:         &computev1.NetworkRoutingConfig{RoutingMode: "REGIONAL"},
	}
	mkFetch := func() func(context.Context, *computev1.Service, string, string) (*computev1.Network, error) {
		return func(_ context.Context, _ *computev1.Service, project, name string) (*computev1.Network, error) {
			assert.Equal(t, "my-project", project)
			assert.Equal(t, "vpc-prod", name)
			return net, nil
		}
	}
	enrichEnr := computeNetworkEnricher{fetch: mkFetch()}
	byIDEnr := computeNetworkEnricher{fetch: mkFetch()}

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_compute_network",
			ImportID: "projects/my-project/global/networks/vpc-prod",
			Address:  "google_compute_network.vpc_prod",
		},
	}
	require.NoError(t, enrichEnr.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}))

	id := &imported.ResourceIdentity{
		Type:     "google_compute_network",
		ImportID: "projects/my-project/global/networks/vpc-prod",
		Address:  "google_compute_network.vpc_prod",
	}
	raw, err := byIDEnr.EnrichByID(context.Background(), id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))

	decoded, err := generated.UnmarshalAttrs("google_compute_network", raw)
	require.NoError(t, err)
	gn, ok := decoded.(*generated.GoogleComputeNetwork)
	require.True(t, ok)
	require.NotNil(t, gn.Name)
	assert.Equal(t, "vpc-prod", *gn.Name.Literal)
	require.NotNil(t, gn.Project)
	assert.Equal(t, "my-project", *gn.Project.Literal)
}
