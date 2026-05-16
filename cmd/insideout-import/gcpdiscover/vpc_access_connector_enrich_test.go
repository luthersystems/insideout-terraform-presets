package gcpdiscover

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	vpcaccessv1 "google.golang.org/api/vpcaccess/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestVPCAccessConnectorEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "google_vpc_access_connector", newVPCAccessConnectorEnricher().ResourceType())
}

func TestVPCAccessConnectorEnricher_NilClient_ReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newVPCAccessConnectorEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      vpcAccessConnectorTFType,
			ProjectID: "my-project",
			Location:  "us-central1",
			NameHint:  "my-connector",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{VPCAccess: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestVPCAccessConnectorEnricher_NotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	e := &vpcAccessConnectorEnricher{
		fetch: func(_ context.Context, _ *vpcaccessv1.Service, _ string) (*vpcaccessv1.Connector, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{
		Type:      vpcAccessConnectorTFType,
		ProjectID: "my-project",
		Location:  "us-central1",
		NameHint:  "my-connector",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{VPCAccess: &vpcaccessv1.Service{}})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestVPCAccessConnectorEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	conn := &vpcaccessv1.Connector{
		Name:              "projects/my-project/locations/us-central1/connectors/my-connector",
		IpCidrRange:       "10.8.0.0/28",
		MachineType:       "e2-micro",
		MaxInstances:      10,
		MaxThroughput:     1000,
		MinInstances:      2,
		MinThroughput:     200,
		Network:           "default",
		State:             "READY",
		ConnectedProjects: []string{"my-project"},
		Subnet: &vpcaccessv1.Subnet{
			Name:      "my-subnet",
			ProjectId: "my-project",
		},
	}
	e := &vpcAccessConnectorEnricher{
		fetch: func(_ context.Context, _ *vpcaccessv1.Service, name string) (*vpcaccessv1.Connector, error) {
			assert.Equal(t, "projects/my-project/locations/us-central1/connectors/my-connector", name)
			return conn, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      vpcAccessConnectorTFType,
			ProjectID: "my-project",
			Location:  "us-central1",
			NameHint:  "my-connector",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{VPCAccess: &vpcaccessv1.Service{}}))

	decoded, err := generated.UnmarshalAttrs("google_vpc_access_connector", ir.Attrs)
	require.NoError(t, err)
	v, ok := decoded.(*generated.GoogleVPCAccessConnector)
	require.True(t, ok)
	require.NotNil(t, v.Name)
	assert.Equal(t, "my-connector", *v.Name.Literal)
	require.NotNil(t, v.Project)
	assert.Equal(t, "my-project", *v.Project.Literal)
	require.NotNil(t, v.Region)
	assert.Equal(t, "us-central1", *v.Region.Literal)
	require.NotNil(t, v.IpCIDRRange)
	assert.Equal(t, "10.8.0.0/28", *v.IpCIDRRange.Literal)
	require.NotNil(t, v.MachineType)
	assert.Equal(t, "e2-micro", *v.MachineType.Literal)
	require.NotNil(t, v.State)
	assert.Equal(t, "READY", *v.State.Literal)
	require.Len(t, v.Subnet, 1)
	require.NotNil(t, v.Subnet[0].Name)
	assert.Equal(t, "my-subnet", *v.Subnet[0].Name.Literal)
}

func TestVPCAccessConnectorEnricher_EnrichByID_MirrorsEnrich(t *testing.T) {
	t.Parallel()
	conn := &vpcaccessv1.Connector{State: "READY", IpCidrRange: "10.8.0.0/28"}
	mkFetch := func() func(context.Context, *vpcaccessv1.Service, string) (*vpcaccessv1.Connector, error) {
		return func(_ context.Context, _ *vpcaccessv1.Service, _ string) (*vpcaccessv1.Connector, error) {
			return conn, nil
		}
	}
	enrichE := &vpcAccessConnectorEnricher{fetch: mkFetch()}
	byIDE := &vpcAccessConnectorEnricher{fetch: mkFetch()}

	id := imported.ResourceIdentity{
		Type:      vpcAccessConnectorTFType,
		ProjectID: "p", Location: "r", NameHint: "n",
	}
	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, enrichE.Enrich(context.Background(), ir, EnrichClients{VPCAccess: &vpcaccessv1.Service{}}))

	raw, err := byIDE.EnrichByID(context.Background(), &id, EnrichClients{VPCAccess: &vpcaccessv1.Service{}})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestVPCAccessConnectorEnricher_DerivesFromImportID(t *testing.T) {
	t.Parallel()
	var gotName string
	e := &vpcAccessConnectorEnricher{
		fetch: func(_ context.Context, _ *vpcaccessv1.Service, name string) (*vpcaccessv1.Connector, error) {
			gotName = name
			return &vpcaccessv1.Connector{}, nil
		},
	}
	// Only ImportID populated — derive project/region/name.
	id := &imported.ResourceIdentity{
		Type:     vpcAccessConnectorTFType,
		ImportID: "projects/their-project/locations/us-east1/connectors/their-conn",
	}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{VPCAccess: &vpcaccessv1.Service{}})
	require.NoError(t, err)
	assert.Equal(t, "projects/their-project/locations/us-east1/connectors/their-conn", gotName)
}

func TestVPCAccessConnectorEnricher_FallbackProject(t *testing.T) {
	t.Parallel()
	var gotName string
	e := &vpcAccessConnectorEnricher{
		fetch: func(_ context.Context, _ *vpcaccessv1.Service, name string) (*vpcaccessv1.Connector, error) {
			gotName = name
			return &vpcaccessv1.Connector{}, nil
		},
	}
	// No Identity.ProjectID — fall back to EnrichClients.ProjectID.
	id := &imported.ResourceIdentity{
		Type:     vpcAccessConnectorTFType,
		Location: "us-west1",
		NameHint: "n",
	}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{VPCAccess: &vpcaccessv1.Service{}, ProjectID: "fallback-project"})
	require.NoError(t, err)
	assert.Equal(t, "projects/fallback-project/locations/us-west1/connectors/n", gotName)
}

func TestVPCAccessConnectorEnricher_CannotDeriveProjectRegionOrName(t *testing.T) {
	t.Parallel()
	e := &vpcAccessConnectorEnricher{
		fetch: func(_ context.Context, _ *vpcaccessv1.Service, _ string) (*vpcaccessv1.Connector, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	// Bare Identity — no project, no region, no name.
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: vpcAccessConnectorTFType}}
	err := e.Enrich(context.Background(), ir, EnrichClients{VPCAccess: &vpcaccessv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive project/region/name")
}

func TestVPCAccessConnectorEnricher_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newVPCAccessConnectorEnricher().(*vpcAccessConnectorEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{VPCAccess: &vpcaccessv1.Service{}})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestVPCAccessConnectorEnricher_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := &vpcAccessConnectorEnricher{
		fetch: func(_ context.Context, _ *vpcaccessv1.Service, _ string) (*vpcaccessv1.Connector, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{
		Type:      vpcAccessConnectorTFType,
		ProjectID: "p", Location: "r", NameHint: "n",
	}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{VPCAccess: &vpcaccessv1.Service{}})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
	assert.Equal(t, http.StatusForbidden, gerr.Code)
}

func TestParseVPCAccessConnectorImportID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		in          string
		wantProject string
		wantRegion  string
		wantName    string
	}{
		{"empty", "", "", "", ""},
		{"full path", "projects/p/locations/r/connectors/c", "p", "r", "c"},
		{"trailing path segment", "projects/p/locations/r/connectors/c/extra", "p", "r", "c"},
		// Parser requires a leading slash for /locations/ — bare "locations/r/..."
		// at index 0 returns no region.
		{"no project segment (no leading slash before locations)", "locations/r/connectors/c", "", "", "c"},
		// /locations/ found but the region segment has no trailing slash, so
		// the parser can't slice it out. /connectors/ also absent.
		{"missing trailing-slash after region", "projects/p/locations/r", "p", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project, region, name := parseVPCAccessConnectorImportID(tc.in)
			assert.Equal(t, tc.wantProject, project)
			assert.Equal(t, tc.wantRegion, region)
			assert.Equal(t, tc.wantName, name)
		})
	}
}
