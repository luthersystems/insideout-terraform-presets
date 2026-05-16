package gcpdiscover

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	servicenetworkingv1 "google.golang.org/api/servicenetworking/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestServiceNetworkingConnectionEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_service_networking_connection", newServiceNetworkingConnectionEnricher().ResourceType())
}

func TestServiceNetworkingConnectionEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newServiceNetworkingConnectionEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: serviceNetworkingConnectionTFType,
			NativeIDs: map[string]string{
				"network": "projects/my-project/global/networks/default",
				"service": "servicenetworking.googleapis.com",
			},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{ServiceNetworking: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestServiceNetworkingConnectionEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &serviceNetworkingConnectionEnricher{
		fetch: func(_ context.Context, _ *servicenetworkingv1.APIService, _, _ string) ([]*servicenetworkingv1.Connection, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{
		Type: serviceNetworkingConnectionTFType,
		NativeIDs: map[string]string{
			"network": "projects/my-project/global/networks/default",
			"service": "servicenetworking.googleapis.com",
		},
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestServiceNetworkingConnectionEnricher_NoMatchingConnection_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &serviceNetworkingConnectionEnricher{
		fetch: func(_ context.Context, _ *servicenetworkingv1.APIService, _, _ string) ([]*servicenetworkingv1.Connection, error) {
			// Returns a list but no entry matches the requested network.
			return []*servicenetworkingv1.Connection{
				{Network: "projects/other/global/networks/other"},
			}, nil
		},
	}
	id := &imported.ResourceIdentity{
		Type: serviceNetworkingConnectionTFType,
		NativeIDs: map[string]string{
			"network": "projects/my-project/global/networks/default",
			"service": "servicenetworking.googleapis.com",
		},
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestServiceNetworkingConnectionEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	conn := &servicenetworkingv1.Connection{
		Network:               "projects/my-project/global/networks/default",
		ReservedPeeringRanges: []string{"alloc-a", "alloc-b"},
		Peering:               "servicenetworking-googleapis-com",
	}
	e := &serviceNetworkingConnectionEnricher{
		fetch: func(_ context.Context, _ *servicenetworkingv1.APIService, parent, network string) ([]*servicenetworkingv1.Connection, error) {
			assert.Equal(t, "services/servicenetworking.googleapis.com", parent)
			assert.Equal(t, "projects/my-project/global/networks/default", network)
			return []*servicenetworkingv1.Connection{conn}, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: serviceNetworkingConnectionTFType,
			NativeIDs: map[string]string{
				"network": "projects/my-project/global/networks/default",
				"service": "servicenetworking.googleapis.com",
			},
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}}))

	decoded, err := generated.UnmarshalAttrs("google_service_networking_connection", ir.Attrs)
	require.NoError(t, err)
	sn, ok := decoded.(*generated.GoogleServiceNetworkingConnection)
	require.True(t, ok)
	require.NotNil(t, sn.Network)
	assert.Equal(t, "projects/my-project/global/networks/default", *sn.Network.Literal)
	require.NotNil(t, sn.Service)
	assert.Equal(t, "servicenetworking.googleapis.com", *sn.Service.Literal)
	require.Len(t, sn.ReservedPeeringRanges, 2)
	assert.Equal(t, "alloc-a", *sn.ReservedPeeringRanges[0].Literal)
	assert.Equal(t, "alloc-b", *sn.ReservedPeeringRanges[1].Literal)
}

func TestServiceNetworkingConnectionEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	conn := &servicenetworkingv1.Connection{
		Network:               "projects/p/global/networks/default",
		ReservedPeeringRanges: []string{"alloc"},
	}
	mkFetch := func() func(context.Context, *servicenetworkingv1.APIService, string, string) ([]*servicenetworkingv1.Connection, error) {
		return func(_ context.Context, _ *servicenetworkingv1.APIService, _, _ string) ([]*servicenetworkingv1.Connection, error) {
			return []*servicenetworkingv1.Connection{conn}, nil
		}
	}
	enrichE := &serviceNetworkingConnectionEnricher{fetch: mkFetch()}
	byIDE := &serviceNetworkingConnectionEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{
		Type: serviceNetworkingConnectionTFType,
		NativeIDs: map[string]string{
			"network": "projects/p/global/networks/default",
			"service": "servicenetworking.googleapis.com",
		},
	}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestServiceNetworkingConnectionEnricher_DerivesFromImportID(t *testing.T) {
	t.Parallel()
	var gotNetwork, gotParent string
	e := &serviceNetworkingConnectionEnricher{
		fetch: func(_ context.Context, _ *servicenetworkingv1.APIService, parent, network string) ([]*servicenetworkingv1.Connection, error) {
			gotParent = parent
			gotNetwork = network
			return []*servicenetworkingv1.Connection{{Network: network}}, nil
		},
	}
	// Only ImportID populated — derive network + service.
	id := &imported.ResourceIdentity{
		Type:     serviceNetworkingConnectionTFType,
		ImportID: "projects/p/global/networks/default:servicenetworking.googleapis.com",
	}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}})
	require.NoError(t, err)
	assert.Equal(t, "projects/p/global/networks/default", gotNetwork)
	assert.Equal(t, "services/servicenetworking.googleapis.com", gotParent)
}

func TestServiceNetworkingConnectionEnricher_CannotDeriveNetworkOrService(t *testing.T) {
	t.Parallel()
	e := &serviceNetworkingConnectionEnricher{
		fetch: func(_ context.Context, _ *servicenetworkingv1.APIService, _, _ string) ([]*servicenetworkingv1.Connection, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	// Bare Identity — no native IDs, no ImportID.
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: serviceNetworkingConnectionTFType}}
	err := e.Enrich(context.Background(), ir, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive network/service")
}

func TestServiceNetworkingConnectionEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newServiceNetworkingConnectionEnricher().(*serviceNetworkingConnectionEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestServiceNetworkingConnectionEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := &serviceNetworkingConnectionEnricher{
		fetch: func(_ context.Context, _ *servicenetworkingv1.APIService, _, _ string) ([]*servicenetworkingv1.Connection, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{
		Type: serviceNetworkingConnectionTFType,
		NativeIDs: map[string]string{
			"network": "projects/p/global/networks/default",
			"service": "servicenetworking.googleapis.com",
		},
	}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{ServiceNetworking: &servicenetworkingv1.APIService{}})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
	assert.Equal(t, http.StatusForbidden, gerr.Code)
}

func TestParseServiceNetworkingConnectionImportID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		in          string
		wantNetwork string
		wantService string
	}{
		{"empty", "", "", ""},
		{"colon-separated", "projects/p/global/networks/default:servicenetworking.googleapis.com", "projects/p/global/networks/default", "servicenetworking.googleapis.com"},
		{"no colon treated as bare network", "projects/p/global/networks/default", "projects/p/global/networks/default", ""},
		{"leading colon yields empty network", ":servicenetworking.googleapis.com", "", "servicenetworking.googleapis.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			network, service := parseServiceNetworkingConnectionImportID(tc.in)
			assert.Equal(t, tc.wantNetwork, network)
			assert.Equal(t, tc.wantService, service)
		})
	}
}

func TestServiceNetworkingConnectionParent(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "services/-", serviceNetworkingConnectionParent(""))
	assert.Equal(t, "services/servicenetworking.googleapis.com", serviceNetworkingConnectionParent("servicenetworking.googleapis.com"))
	assert.Equal(t, "services/already/prefixed", serviceNetworkingConnectionParent("services/already/prefixed"))
}
