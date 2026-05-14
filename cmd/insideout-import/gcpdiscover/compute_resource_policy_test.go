package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeResourcePolicyFromAsset(t *testing.T) {
	t.Parallel()
	d := newComputeResourcePolicyDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/resourcePolicies/io-foo-snapshot",
			AssetType: "compute.googleapis.com/ResourcePolicy",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	require.Equal(t, "google_compute_resource_policy", got.Identity.Type)
	require.Equal(t, "io-foo-snapshot", got.Identity.NameHint)
	require.Equal(t, "projects/real-proj/regions/us-central1/resourcePolicies/io-foo-snapshot",
		got.Identity.ImportID)
	require.Equal(t, "us-central1", got.Identity.Location)
}

func TestComputeResourcePolicyRecoversRegionFromAssetNameWhenLocationFieldEmpty(t *testing.T) {
	t.Parallel()
	d := newComputeResourcePolicyDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-west1/resourcePolicies/io-foo-snapshot",
			AssetType: "compute.googleapis.com/ResourcePolicy",
		},
		"real-proj")
	assert.Equal(t, "us-west1", got.Identity.Location,
		"region must be recovered from the asset name when Location field empty")
}

func TestComputeResourcePolicyDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeResourcePolicyDiscoverer()
	cases := []struct {
		name, in, wantName, wantRegion string
		wantErr                        error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/regions/us-east1/resourcePolicies/r1",
			wantName: "r1", wantRegion: "us-east1"},
		{name: "import id", in: "projects/p/regions/us-central1/resourcePolicies/r1",
			wantName: "r1", wantRegion: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "missing regions segment", in: "projects/p/resourcePolicies/r1", wantErr: ErrNotSupported},
		{name: "missing resource segment", in: "projects/p/regions/us-east1", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := d.DiscoverByID(context.Background(), nil, tc.in, "real-proj")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantName, got.Identity.NameHint)
			assert.Equal(t, tc.wantRegion, got.Identity.Location)
		})
	}
}
