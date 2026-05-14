package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
)

func TestVPCAccessConnectorListNonCAI_EmitsRowPerConnector(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCAccessConnectorLister{
		connectorsByProject: map[string][]gcpVPCAccessConnector{
			"real-proj": {
				{Name: "io-foo-conn", Region: "us-central1",
					Full:  "projects/real-proj/locations/us-central1/connectors/io-foo-conn",
					State: "READY"},
				{Name: "io-bar-conn", Region: "us-east1",
					Full:  "projects/real-proj/locations/us-east1/connectors/io-bar-conn",
					State: "READY"},
			},
		},
	}
	d := newVPCAccessConnectorDiscoverer(fake).(*vpcAccessConnectorDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Len(t, fake.calls, 1, "single round-trip per project — no per-region fan-out")

	byImport := map[string]string{}
	regionByName := map[string]string{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = r.Identity.NameHint
		regionByName[r.Identity.NameHint] = r.Identity.Location
	}
	assert.Equal(t, "io-foo-conn",
		byImport["projects/real-proj/locations/us-central1/connectors/io-foo-conn"])
	assert.Equal(t, "io-bar-conn",
		byImport["projects/real-proj/locations/us-east1/connectors/io-bar-conn"])
	assert.Equal(t, "us-central1", regionByName["io-foo-conn"],
		"connector Identity.Location must carry the region for UI grouping")
	assert.Equal(t, "us-east1", regionByName["io-bar-conn"])
}

func TestVPCAccessConnectorListNonCAI_RecoversFromFullPathWhenNameRegionMissing(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCAccessConnectorLister{
		connectorsByProject: map[string][]gcpVPCAccessConnector{
			"real-proj": {
				// Only Full is populated — Name/Region should be
				// recovered from the path so a fake (or future
				// Real lister regression) doesn't silently emit
				// empty-id rows.
				{Full: "projects/real-proj/locations/europe-west1/connectors/conn-x", State: "READY"},
			},
		},
	}
	d := newVPCAccessConnectorDiscoverer(fake).(*vpcAccessConnectorDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "conn-x", got[0].Identity.NameHint)
	assert.Equal(t, "europe-west1", got[0].Identity.Location)
	assert.Equal(t, "projects/real-proj/locations/europe-west1/connectors/conn-x",
		got[0].Identity.ImportID)
}

func TestVPCAccessConnectorListNonCAI_ErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("api not enabled")
	fake := &fakeVPCAccessConnectorLister{
		errByProject: map[string]error{"real-proj": want},
	}
	d := newVPCAccessConnectorDiscoverer(fake).(*vpcAccessConnectorDiscoverer)
	_, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	assert.ErrorIs(t, err, want)
}

func TestVPCAccessConnectorListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newVPCAccessConnectorDiscoverer(nil).(*vpcAccessConnectorDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestVPCAccessConnectorListNonCAI_EmptyResultYieldsNil(t *testing.T) {
	t.Parallel()
	fake := &fakeVPCAccessConnectorLister{
		connectorsByProject: map[string][]gcpVPCAccessConnector{"real-proj": {}},
	}
	d := newVPCAccessConnectorDiscoverer(fake).(*vpcAccessConnectorDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil, progress.NopEmitter{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestVPCAccessConnectorImportID(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"projects/p/locations/us-central1/connectors/c",
		vpcAccessConnectorImportID("p", "us-central1", "c"))
}

func TestParseVPCAccessConnectorPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, wantRegion, wantName string
	}{
		{name: "well-formed",
			in:         "projects/p/locations/us-central1/connectors/conn-x",
			wantRegion: "us-central1", wantName: "conn-x"},
		{name: "missing locations",
			in:         "projects/p/connectors/c",
			wantRegion: "", wantName: "c"},
		{name: "missing connectors",
			// Trailing-segment region without a subsequent path
			// component falls through to "" — the GCP API never
			// returns this shape (every Connector path includes the
			// /connectors/<n> tail), but the parse is defensive and
			// fails closed rather than returning a partial result.
			in:         "projects/p/locations/r",
			wantRegion: "", wantName: ""},
		{name: "empty",
			in:         "",
			wantRegion: "", wantName: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotRegion, gotName := parseVPCAccessConnectorPath(tc.in)
			assert.Equal(t, tc.wantRegion, gotRegion)
			assert.Equal(t, tc.wantName, gotName)
		})
	}
}
