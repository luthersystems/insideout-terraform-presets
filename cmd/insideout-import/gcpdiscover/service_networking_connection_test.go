package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// makeNetworkResult builds a minimal ImportedResource for a
// google_compute_network, mimicking the CAI fanout output that
// service_networking_connection reads from priorResults.
func makeNetworkResult(name string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     computeNetworkTFType,
			NameHint: name,
			ImportID: name,
		},
	}
}

func TestServiceNetworkingConnectionListNonCAI_FansOutAcrossNetworks(t *testing.T) {
	t.Parallel()
	netA := "projects/real-proj/global/networks/io-foo-vpc"
	netB := "projects/real-proj/global/networks/io-bar-vpc"
	fake := &fakeServiceNetworkingConnectionLister{
		connectionsByNetwork: map[string][]gcpServiceNetworkingConnection{
			netA: {
				{
					Network:          netA,
					Service:          "services/servicenetworking.googleapis.com",
					Peering:          "servicenetworking-googleapis-com",
					ReservedPeerings: []string{"google-managed-services-io-foo-vpc"},
				},
			},
			netB: {
				{
					Network: netB,
					Service: "services/servicenetworking.googleapis.com",
					Peering: "servicenetworking-googleapis-com",
				},
			},
		},
	}
	d := newServiceNetworkingConnectionDiscoverer(fake).(*serviceNetworkingConnectionDiscoverer)
	prior := []imported.ImportedResource{
		makeNetworkResult("io-foo-vpc"),
		makeNetworkResult("io-bar-vpc"),
		// Non-network priors are skipped (no spurious fanout).
		{Identity: imported.ResourceIdentity{Type: storageBucketTFType, NameHint: "io-foo-bucket"}},
	}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Len(t, fake.calls, 2, "exactly two network priors should trigger fan-out calls")

	byImport := map[string]string{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = r.Identity.NameHint
	}
	// Import-ID per provider docs: "<network>:<service>".
	assert.Contains(t, byImport, netA+":services/servicenetworking.googleapis.com")
	assert.Contains(t, byImport, netB+":services/servicenetworking.googleapis.com")
}

func TestServiceNetworkingConnectionListNonCAI_PerNetworkErrorSoftFails(t *testing.T) {
	t.Parallel()
	netA := "projects/real-proj/global/networks/io-foo-vpc"
	netB := "projects/real-proj/global/networks/io-bar-vpc"
	fake := &fakeServiceNetworkingConnectionLister{
		connectionsByNetwork: map[string][]gcpServiceNetworkingConnection{
			netA: {
				{Network: netA, Service: "services/servicenetworking.googleapis.com"},
			},
		},
		errByNetwork: map[string]error{
			netB: errors.New("network not accessible"),
		},
	}
	d := newServiceNetworkingConnectionDiscoverer(fake).(*serviceNetworkingConnectionDiscoverer)
	prior := []imported.ImportedResource{
		makeNetworkResult("io-foo-vpc"),
		makeNetworkResult("io-bar-vpc"),
	}
	rec := &recordingEmitter{}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior, rec)
	require.NoError(t, err)
	require.Len(t, got, 1, "soft-fail should drop only the failing network, not all of them")
	// Pin which network's connection row survived.
	assert.Equal(t, netA+":services/servicenetworking.googleapis.com", got[0].Identity.ImportID)

	var warns []recordedEvent
	for _, ev := range rec.snapshot() {
		if ev.Kind == "service_warn" {
			warns = append(warns, ev)
		}
	}
	require.Len(t, warns, 1)
	assert.Equal(t, nonCAIServiceSlug, warns[0].Service)
	assert.Contains(t, warns[0].Message, "io-bar-vpc",
		"warn message must name the failing network")
	assert.Contains(t, warns[0].Message, "network not accessible",
		"warn message must include the underlying error")
}

func TestServiceNetworkingConnectionListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newServiceNetworkingConnectionDiscoverer(nil).(*serviceNetworkingConnectionDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "",
		[]imported.ImportedResource{makeNetworkResult("io-foo")}, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestServiceNetworkingConnectionListNonCAI_NoNetworkPriorsYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceNetworkingConnectionLister{}
	d := newServiceNetworkingConnectionDiscoverer(fake).(*serviceNetworkingConnectionDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, fake.calls, "lister must be untouched without network priors")
}

func TestServiceNetworkingConnectionImportID(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"projects/p/global/networks/n:services/servicenetworking.googleapis.com",
		serviceNetworkingConnectionImportID("projects/p/global/networks/n", "services/servicenetworking.googleapis.com"))
}

// TestServiceNetworkingConnectionNetworkPath pins the parent-row
// translation helper. The function is defensive against the parent
// google_compute_network discoverer's ImportID format evolving — without
// direct cases here the ImportID-only branch (line 116-118) ships
// untested and a regression that broke the "/networks/" detection
// would surface only as silent zero-row emissions during the per-
// parent fan-out.
func TestServiceNetworkingConnectionNetworkPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		nameHint  string
		importID  string
		projectID string
		want      string
	}{
		{
			name:      "NameHint set wins",
			nameHint:  "io-foo-vpc",
			importID:  "ignored",
			projectID: "real-proj",
			want:      "projects/real-proj/global/networks/io-foo-vpc",
		},
		{
			name:      "ImportID fallback when NameHint empty",
			nameHint:  "",
			importID:  "projects/other-proj/global/networks/io-bar-vpc",
			projectID: "real-proj",
			want:      "projects/other-proj/global/networks/io-bar-vpc",
		},
		{
			name:      "neither NameHint nor /networks/-shaped ImportID",
			nameHint:  "",
			importID:  "some-bare-id",
			projectID: "real-proj",
			want:      "",
		},
		{
			name:      "everything empty",
			nameHint:  "",
			importID:  "",
			projectID: "real-proj",
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior := imported.ImportedResource{
				Identity: imported.ResourceIdentity{
					Type:     computeNetworkTFType,
					NameHint: tc.nameHint,
					ImportID: tc.importID,
				},
			}
			assert.Equal(t, tc.want, serviceNetworkingConnectionNetworkPath(tc.projectID, prior))
		})
	}
}

func TestServiceNetworkingConnectionNameHint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, network, service, want string
	}{
		{name: "standard", network: "io-foo-vpc", service: "services/servicenetworking.googleapis.com",
			want: "io-foo-vpc-servicenetworking"},
		{name: "missing services prefix", network: "vpc", service: "servicenetworking.googleapis.com",
			want: "vpc-servicenetworking"},
		{name: "empty network", network: "", service: "services/foo.googleapis.com",
			want: "foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, serviceNetworkingConnectionNameHint(tc.network, tc.service))
		})
	}
}
