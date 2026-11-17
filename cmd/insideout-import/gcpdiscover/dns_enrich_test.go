package gcpdiscover

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dnsv1 "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestDNSManagedZoneEnricher_PrivateZoneMapsNestedBlocks(t *testing.T) {
	t.Parallel()
	zone := &dnsv1.ManagedZone{
		Name:         "io-foo-zone",
		DnsName:      "internal.example.",
		Description:  "private zone",
		Visibility:   "private",
		Id:           4831990241185325831,
		CreationTime: "2026-01-01T00:00:00Z",
		NameServers:  []string{"ns-gcp-private.googledomains.com."},
		Labels:       map[string]string{"project": "io-foo", "goog-managed": "x"},
		PrivateVisibilityConfig: &dnsv1.ManagedZonePrivateVisibilityConfig{
			Networks: []*dnsv1.ManagedZonePrivateVisibilityConfigNetwork{
				{NetworkUrl: "https://www.googleapis.com/compute/v1/projects/real-proj/global/networks/vpc"},
			},
		},
		DnssecConfig: &dnsv1.ManagedZoneDnsSecConfig{
			State:        "on",
			NonExistence: "nsec3",
			DefaultKeySpecs: []*dnsv1.DnsKeySpec{
				{Algorithm: "rsasha256", KeyLength: 2048, KeyType: "keySigning"},
			},
		},
		CloudLoggingConfig: &dnsv1.ManagedZoneCloudLoggingConfig{EnableLogging: true},
	}
	var gotProject, gotZone string
	e := &dnsManagedZoneEnricher{fetch: func(_ context.Context, _ *dnsv1.Service, project, z string) (*dnsv1.ManagedZone, error) {
		gotProject, gotZone = project, z
		return zone, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		Type: dnsManagedZoneTFType, ProjectID: "real-proj", NameHint: "io-foo-zone",
	}}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{DNS: &dnsv1.Service{}}))
	assert.Equal(t, "real-proj", gotProject)
	assert.Equal(t, "io-foo-zone", gotZone)

	decoded, err := generated.UnmarshalAttrs(dnsManagedZoneTFType, ir.Attrs)
	require.NoError(t, err)
	v := decoded.(*generated.GoogleDNSManagedZone)
	assert.Equal(t, "io-foo-zone", *v.Name.Literal)
	assert.Equal(t, "real-proj", *v.Project.Literal)
	assert.Equal(t, "internal.example.", *v.DNSName.Literal)
	assert.Equal(t, "private", *v.Visibility.Literal)
	require.Len(t, v.Labels, 1, "goog-* labels must be dropped")
	assert.Equal(t, "io-foo", *v.Labels["project"].Literal)
	require.Len(t, v.PrivateVisibilityConfig, 1)
	require.Len(t, v.PrivateVisibilityConfig[0].Networks, 1)
	assert.Contains(t, *v.PrivateVisibilityConfig[0].Networks[0].NetworkURL.Literal, "/networks/vpc")
	require.Len(t, v.DnssecConfig, 1)
	assert.Equal(t, "on", *v.DnssecConfig[0].State.Literal)
	require.Len(t, v.DnssecConfig[0].DefaultKeySpecs, 1)
	assert.InDelta(t, 2048, *v.DnssecConfig[0].DefaultKeySpecs[0].KeyLength.Literal, 0)
	require.Len(t, v.CloudLoggingConfig, 1)
	assert.True(t, *v.CloudLoggingConfig[0].EnableLogging.Literal)
	// Computed-only fields stay out (decision #5).
	assert.Nil(t, v.ManagedZoneID)
	assert.Nil(t, v.CreationTime)
	assert.Nil(t, v.NameServers)
	assert.Nil(t, v.ID)
}

func TestDNSManagedZoneEnricher_DerivesFromImportID(t *testing.T) {
	t.Parallel()
	var gotProject, gotZone string
	e := &dnsManagedZoneEnricher{fetch: func(_ context.Context, _ *dnsv1.Service, project, z string) (*dnsv1.ManagedZone, error) {
		gotProject, gotZone = project, z
		return &dnsv1.ManagedZone{DnsName: "example.com."}, nil
	}}
	id := &imported.ResourceIdentity{Type: dnsManagedZoneTFType, ImportID: "projects/p1/managedZones/z1"}
	_, err := e.EnrichByID(context.Background(), id, EnrichClients{DNS: &dnsv1.Service{}})
	require.NoError(t, err)
	assert.Equal(t, "p1", gotProject)
	assert.Equal(t, "z1", gotZone)
}

func TestDNSManagedZoneEnricher_Errors(t *testing.T) {
	t.Parallel()
	id := &imported.ResourceIdentity{Type: dnsManagedZoneTFType, ProjectID: "p", NameHint: "z"}

	_, err := newDNSManagedZoneEnricher().(ByIDEnricher).EnrichByID(context.Background(), id, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)

	notFound := &dnsManagedZoneEnricher{fetch: func(context.Context, *dnsv1.Service, string, string) (*dnsv1.ManagedZone, error) {
		return nil, &googleapi.Error{Code: http.StatusNotFound}
	}}
	_, err = notFound.EnrichByID(context.Background(), id, EnrichClients{DNS: &dnsv1.Service{}})
	require.ErrorIs(t, err, ErrNotFound)

	_, err = notFound.EnrichByID(context.Background(), &imported.ResourceIdentity{Type: dnsManagedZoneTFType}, EnrichClients{DNS: &dnsv1.Service{}})
	require.Error(t, err, "no project/zone derivable")
}

func TestDNSRecordSetEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	var got [4]string
	e := &dnsRecordSetEnricher{fetch: func(_ context.Context, _ *dnsv1.Service, project, zone, name, typ string) (*dnsv1.ResourceRecordSet, error) {
		got = [4]string{project, zone, name, typ}
		return &dnsv1.ResourceRecordSet{Name: name, Type: typ, Ttl: 300, Rrdatas: []string{"203.0.113.10", "203.0.113.11"}}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		Type:      dnsRecordSetTFType,
		ProjectID: "real-proj",
		NativeIDs: map[string]string{"managed_zone": "zone-a", "record_name": "www.a.example.", "record_type": "A"},
	}}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{DNS: &dnsv1.Service{}}))
	assert.Equal(t, [4]string{"real-proj", "zone-a", "www.a.example.", "A"}, got)

	decoded, err := generated.UnmarshalAttrs(dnsRecordSetTFType, ir.Attrs)
	require.NoError(t, err)
	v := decoded.(*generated.GoogleDNSRecordSet)
	assert.Equal(t, "zone-a", *v.ManagedZone.Literal)
	assert.Equal(t, "www.a.example.", *v.Name.Literal)
	assert.Equal(t, "A", *v.Type_.Literal)
	assert.Equal(t, "real-proj", *v.Project.Literal)
	assert.InDelta(t, 300, *v.TTL.Literal, 0)
	require.Len(t, v.Rrdatas, 2)
	assert.Equal(t, "203.0.113.10", *v.Rrdatas[0].Literal)
}

func TestDNSRecordSetEnricher_DerivesFromImportIDAndMirrorsEnrich(t *testing.T) {
	t.Parallel()
	var got [4]string
	e := &dnsRecordSetEnricher{fetch: func(_ context.Context, _ *dnsv1.Service, project, zone, name, typ string) (*dnsv1.ResourceRecordSet, error) {
		got = [4]string{project, zone, name, typ}
		return &dnsv1.ResourceRecordSet{Ttl: 60, Rrdatas: []string{"\"v=spf1 -all\""}}, nil
	}}
	id := imported.ResourceIdentity{Type: dnsRecordSetTFType, ImportID: "projects/p1/managedZones/z1/rrsets/z1.example./TXT"}
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{DNS: &dnsv1.Service{}})
	require.NoError(t, err)
	assert.Equal(t, [4]string{"p1", "z1", "z1.example.", "TXT"}, got)

	ir := &imported.ImportedResource{Identity: id}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{DNS: &dnsv1.Service{}}))
	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

func TestDNSRecordSetEnricher_Errors(t *testing.T) {
	t.Parallel()
	id := &imported.ResourceIdentity{Type: dnsRecordSetTFType, ImportID: "projects/p/managedZones/z/rrsets/a.example./A"}

	_, err := newDNSRecordSetEnricher().(ByIDEnricher).EnrichByID(context.Background(), id, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)

	notFound := &dnsRecordSetEnricher{fetch: func(context.Context, *dnsv1.Service, string, string, string, string) (*dnsv1.ResourceRecordSet, error) {
		return nil, &googleapi.Error{Code: http.StatusNotFound}
	}}
	_, err = notFound.EnrichByID(context.Background(), id, EnrichClients{DNS: &dnsv1.Service{}})
	require.ErrorIs(t, err, ErrNotFound)

	_, err = notFound.EnrichByID(context.Background(), &imported.ResourceIdentity{Type: dnsRecordSetTFType}, EnrichClients{DNS: &dnsv1.Service{}})
	require.Error(t, err, "no zone/name/type derivable")
}

// A routed record set keeps its answers under routing_policy and has
// no top-level rrdatas. The mapper must carry the policy over, or the
// payload would describe an empty record set.
func TestMapDNSRecordSet_RoutingPolicy(t *testing.T) {
	t.Parallel()
	ilb := &dnsv1.RRSetRoutingPolicyLoadBalancerTarget{
		IpAddress: "10.0.0.5", IpProtocol: "tcp", LoadBalancerType: "regionalL4ilb",
		NetworkUrl: "https://www.googleapis.com/compute/v1/projects/p/global/networks/n",
		Port:       "80", Project: "p", Region: "us-central1",
	}

	t.Run("wrr", func(t *testing.T) {
		t.Parallel()
		got, err := mapDNSRecordSet(&dnsv1.ResourceRecordSet{Ttl: 30, RoutingPolicy: &dnsv1.RRSetRoutingPolicy{
			Wrr: &dnsv1.RRSetRoutingPolicyWrrPolicy{Items: []*dnsv1.RRSetRoutingPolicyWrrPolicyWrrPolicyItem{
				{Weight: 0.8, Rrdatas: []string{"203.0.113.1"}},
				{Weight: 0.2, HealthCheckedTargets: &dnsv1.RRSetRoutingPolicyHealthCheckTargets{
					InternalLoadBalancers: []*dnsv1.RRSetRoutingPolicyLoadBalancerTarget{ilb},
				}},
			}},
		}}, "p", "z", "w.example.", "A")
		require.NoError(t, err)
		assert.Empty(t, got.Rrdatas)
		require.Len(t, got.RoutingPolicy, 1)
		wrr := got.RoutingPolicy[0].Wrr
		require.Len(t, wrr, 2)
		assert.InDelta(t, 0.8, *wrr[0].Weight.Literal, 0)
		assert.Equal(t, "203.0.113.1", *wrr[0].Rrdatas[0].Literal)
		require.Len(t, wrr[1].HealthCheckedTargets, 1)
		lb := wrr[1].HealthCheckedTargets[0].InternalLoadBalancers
		require.Len(t, lb, 1)
		assert.Equal(t, "10.0.0.5", *lb[0].IpAddress.Literal)
		assert.Equal(t, "regionalL4ilb", *lb[0].LoadBalancerType.Literal)
		assert.Equal(t, "us-central1", *lb[0].Region.Literal)
	})

	t.Run("geo", func(t *testing.T) {
		t.Parallel()
		got, err := mapDNSRecordSet(&dnsv1.ResourceRecordSet{RoutingPolicy: &dnsv1.RRSetRoutingPolicy{
			Geo: &dnsv1.RRSetRoutingPolicyGeoPolicy{EnableFencing: true, Items: []*dnsv1.RRSetRoutingPolicyGeoPolicyGeoPolicyItem{
				{Location: "us-east1", Rrdatas: []string{"203.0.113.2"}},
			}},
		}}, "p", "z", "g.example.", "A")
		require.NoError(t, err)
		require.Len(t, got.RoutingPolicy, 1)
		rp := got.RoutingPolicy[0]
		assert.True(t, *rp.EnableGeoFencing.Literal)
		require.Len(t, rp.Geo, 1)
		assert.Equal(t, "us-east1", *rp.Geo[0].Location.Literal)
		assert.Equal(t, "203.0.113.2", *rp.Geo[0].Rrdatas[0].Literal)
	})

	t.Run("primary_backup", func(t *testing.T) {
		t.Parallel()
		got, err := mapDNSRecordSet(&dnsv1.ResourceRecordSet{RoutingPolicy: &dnsv1.RRSetRoutingPolicy{
			PrimaryBackup: &dnsv1.RRSetRoutingPolicyPrimaryBackupPolicy{
				TrickleTraffic: 0.1,
				PrimaryTargets: &dnsv1.RRSetRoutingPolicyHealthCheckTargets{InternalLoadBalancers: []*dnsv1.RRSetRoutingPolicyLoadBalancerTarget{ilb}},
				BackupGeoTargets: &dnsv1.RRSetRoutingPolicyGeoPolicy{EnableFencing: true, Items: []*dnsv1.RRSetRoutingPolicyGeoPolicyGeoPolicyItem{
					{Location: "us-west1", Rrdatas: []string{"203.0.113.3"}},
				}},
			},
		}}, "p", "z", "pb.example.", "A")
		require.NoError(t, err)
		require.Len(t, got.RoutingPolicy, 1)
		pb := got.RoutingPolicy[0].PrimaryBackup
		require.Len(t, pb, 1)
		assert.InDelta(t, 0.1, *pb[0].TrickleRatio.Literal, 0)
		assert.True(t, *pb[0].EnableGeoFencingForBackups.Literal)
		require.Len(t, pb[0].Primary, 1)
		require.Len(t, pb[0].Primary[0].InternalLoadBalancers, 1)
		assert.Equal(t, "10.0.0.5", *pb[0].Primary[0].InternalLoadBalancers[0].IpAddress.Literal)
		require.Len(t, pb[0].BackupGeo, 1)
		assert.Equal(t, "us-west1", *pb[0].BackupGeo[0].Location.Literal)
	})
}

// Parts of a routing policy the pinned provider schema has no field for
// (a policy-level health check, external endpoints) cannot be carried
// over. Dropping them would hand back a payload that changes the record
// on apply, so the enricher fails the resource instead.
func TestDNSRecordSetEnricher_RoutingPolicyUnsupportedFieldsFail(t *testing.T) {
	t.Parallel()
	cases := map[string]*dnsv1.RRSetRoutingPolicy{
		"health_check": {
			HealthCheck: "projects/p/global/healthChecks/hc",
			Wrr:         &dnsv1.RRSetRoutingPolicyWrrPolicy{Items: []*dnsv1.RRSetRoutingPolicyWrrPolicyWrrPolicyItem{{Weight: 1, Rrdatas: []string{"203.0.113.1"}}}},
		},
		"external_endpoints": {
			Wrr: &dnsv1.RRSetRoutingPolicyWrrPolicy{Items: []*dnsv1.RRSetRoutingPolicyWrrPolicyWrrPolicyItem{{
				Weight:               1,
				HealthCheckedTargets: &dnsv1.RRSetRoutingPolicyHealthCheckTargets{ExternalEndpoints: []string{"203.0.113.9"}},
			}}},
		},
		"no_policy_kind": {},
	}
	for name, rp := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := &dnsRecordSetEnricher{fetch: func(context.Context, *dnsv1.Service, string, string, string, string) (*dnsv1.ResourceRecordSet, error) {
				return &dnsv1.ResourceRecordSet{RoutingPolicy: rp}, nil
			}}
			id := &imported.ResourceIdentity{Type: dnsRecordSetTFType, ImportID: "projects/p/managedZones/z/rrsets/a.example./A"}
			_, err := e.EnrichByID(context.Background(), id, EnrichClients{DNS: &dnsv1.Service{}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "routing_policy")
		})
	}
}
