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

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

var (
	_ AttributeEnricher = (*computeRouterEnricher)(nil)
	_ ByIDEnricher      = (*computeRouterEnricher)(nil)
)

func routerIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_compute_router",
		NameHint: "io-foo-router",
		Address:  "google_compute_router.io_foo_router",
		ImportID: "projects/my-project/regions/us-central1/routers/io-foo-router",
		Location: "us-central1",
		NativeIDs: map[string]string{
			"asset_name": "//compute.googleapis.com/projects/my-project/regions/us-central1/routers/io-foo-router",
			"self_link":  "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/routers/io-foo-router",
		},
	}
}

func TestMapComputeRouter_Minimal(t *testing.T) {
	t.Parallel()
	src := &computev1.Router{
		Name:    "io-foo-router",
		Network: "projects/my-project/global/networks/default",
	}
	got := mapComputeRouter(src, "my-project", "us-central1")

	require.NotNil(t, got.Name)
	assert.Equal(t, "io-foo-router", *got.Name.Literal)
	require.NotNil(t, got.Network)
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)
	require.NotNil(t, got.Region)
	assert.Equal(t, "us-central1", *got.Region.Literal)
	assert.Empty(t, got.Bgp, "no BGP must not emit empty block")
}

func TestMapComputeRouter_WithBGP(t *testing.T) {
	t.Parallel()
	src := &computev1.Router{
		Name:    "io-foo-router",
		Network: "projects/my-project/global/networks/default",
		Bgp: &computev1.RouterBgp{
			Asn:               65000,
			AdvertiseMode:     "CUSTOM",
			AdvertisedGroups:  []string{"ALL_SUBNETS"},
			KeepaliveInterval: 30,
			AdvertisedIpRanges: []*computev1.RouterAdvertisedIpRange{
				{Range: "10.0.0.0/8", Description: "VPN"},
			},
		},
	}
	got := mapComputeRouter(src, "my-project", "us-central1")

	require.Len(t, got.Bgp, 1)
	require.NotNil(t, got.Bgp[0].Asn)
	assert.InDelta(t, float64(65000), *got.Bgp[0].Asn.Literal, 0.001)
	require.NotNil(t, got.Bgp[0].AdvertiseMode)
	assert.Equal(t, "CUSTOM", *got.Bgp[0].AdvertiseMode.Literal)
	require.Len(t, got.Bgp[0].AdvertisedGroups, 1)
	require.Len(t, got.Bgp[0].AdvertisedIpRanges, 1)
	require.NotNil(t, got.Bgp[0].AdvertisedIpRanges[0].Range_)
	assert.Equal(t, "10.0.0.0/8", *got.Bgp[0].AdvertisedIpRanges[0].Range_.Literal)
}

func TestComputeRouterEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newComputeRouterEnricher()
	ir := &imported.ImportedResource{Identity: routerIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestComputeRouterEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := computeRouterEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Router, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound}
		},
	}
	ir := &imported.ImportedResource{Identity: routerIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestComputeRouterEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 forbidden")
	e := computeRouterEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Router, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: routerIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestComputeRouterEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	r := &computev1.Router{
		Name:    "io-foo-router",
		Network: "projects/my-project/global/networks/default",
		Bgp:     &computev1.RouterBgp{Asn: 65000, AdvertiseMode: "DEFAULT"},
	}
	var gotProj, gotRegion, gotName string
	e := computeRouterEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, p, region, name string) (*computev1.Router, error) {
			gotProj, gotRegion, gotName = p, region, name
			return r, nil
		},
	}
	ir := &imported.ImportedResource{Identity: routerIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "my-project", gotProj)
	assert.Equal(t, "us-central1", gotRegion)
	assert.Equal(t, "io-foo-router", gotName)

	decoded, err := generated.UnmarshalAttrs("google_compute_router", ir.Attrs)
	require.NoError(t, err)
	gr, ok := decoded.(*generated.GoogleComputeRouter)
	require.True(t, ok)
	require.NotNil(t, gr.Name)
	assert.Equal(t, "io-foo-router", *gr.Name.Literal)
	require.Len(t, gr.Bgp, 1)
}

func TestComputeRouterEnrichByID(t *testing.T) {
	t.Parallel()
	e := computeRouterEnricher{
		fetch: func(_ context.Context, _ *computev1.Service, _, _, _ string) (*computev1.Router, error) {
			return &computev1.Router{Name: "io-foo-router"}, nil
		},
	}
	id := routerIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{Compute: &computev1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	var p map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &p))
}

func TestComputeRouterEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newComputeRouterEnricher().(*computeRouterEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{Compute: &computev1.Service{}, ProjectID: "p"})
	require.Error(t, err)
}

func TestComputeRouterRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_compute_router"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_compute_router", enr.ResourceType())
}
