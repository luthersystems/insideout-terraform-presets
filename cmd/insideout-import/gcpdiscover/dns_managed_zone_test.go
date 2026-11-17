package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CAI names a managed zone by its numeric ID; the import ID must use the
// zone name from the display name (#931).
func TestDNSManagedZoneFromAsset_UsesDisplayNameNotNumericID(t *testing.T) {
	t.Parallel()
	d := newDNSManagedZoneDiscoverer()
	got := d.FromAsset(addressBook{}, gcpAssetResult{
		Name:        "//dns.googleapis.com/projects/real-proj/managedZones/4831990241185325831",
		AssetType:   dnsManagedZoneAssetType,
		DisplayName: "io-foo-example-com",
		Labels:      map[string]string{"project": "io-foo"},
	}, "real-proj")
	assert.Equal(t, "google_dns_managed_zone", got.Identity.Type)
	assert.Equal(t, "io-foo-example-com", got.Identity.NameHint)
	assert.Equal(t, "projects/real-proj/managedZones/io-foo-example-com", got.Identity.ImportID)
	assert.Equal(t, "real-proj", got.Identity.ProjectID)
	assert.Empty(t, got.Identity.Location, "managed zones are global")
	assert.Equal(t, "//dns.googleapis.com/projects/real-proj/managedZones/4831990241185325831", got.Identity.NativeIDs["asset_name"],
		"asset_name must keep the real CAI name so a CAI lookup by name still matches")
	assert.Equal(t, map[string]string{"project": "io-foo"}, got.Identity.Tags)
}

func TestDNSManagedZoneFromAsset_FallsBackToNameSegment(t *testing.T) {
	t.Parallel()
	got := newDNSManagedZoneDiscoverer().FromAsset(addressBook{}, gcpAssetResult{
		Name: "//dns.googleapis.com/projects/real-proj/managedZones/io-foo-zone",
	}, "real-proj")
	assert.Equal(t, "projects/real-proj/managedZones/io-foo-zone", got.Identity.ImportID)
}

// With no display name and a numeric last segment there is no zone name
// to import by, so the row is skipped (zero Identity.Type) rather than
// emitted with an import ID Terraform would reject.
func TestDNSManagedZoneFromAsset_SkipsNumericIDWithoutDisplayName(t *testing.T) {
	t.Parallel()
	got := newDNSManagedZoneDiscoverer().FromAsset(addressBook{}, gcpAssetResult{
		Name: "//dns.googleapis.com/projects/real-proj/managedZones/4831990241185325831",
	}, "real-proj")
	assert.Empty(t, got.Identity.Type)
}

// End to end through DiscoverTypes: the zone is searched with the
// labels.project clause and translated with its display name.
func TestDNSManagedZone_DiscoverTypes_LabelScopedSearch(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{results: []gcpAssetResult{{
		Name:        "//dns.googleapis.com/projects/real-proj/managedZones/123",
		AssetType:   dnsManagedZoneAssetType,
		DisplayName: "io-foo-zone",
	}}}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})
	got, err := g.DiscoverTypes(context.Background(), []string{dnsManagedZoneTFType}, DiscoverArgs{Project: "io-foo"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "projects/real-proj/managedZones/io-foo-zone", got[0].Identity.ImportID)
	require.Len(t, fake.calls, 1)
	assert.Equal(t, []string{dnsManagedZoneAssetType}, fake.calls[0].assetTypes)
	assert.Contains(t, fake.calls[0].query, "labels.project:io-foo")
}

func TestDNSManagedZoneDiscoverByID(t *testing.T) {
	t.Parallel()
	numeric := "//dns.googleapis.com/projects/p/managedZones/987"
	cases := []struct {
		name, in   string
		searcher   bool // give the case a fake CAI searcher that knows zone 987
		wantImport string
		wantErr    error
	}{
		{name: "import id", in: "projects/p/managedZones/my-zone", wantImport: "projects/real-proj/managedZones/my-zone"},
		{name: "bare name", in: "my-zone", wantImport: "projects/real-proj/managedZones/my-zone"},
		{name: "asset name with zone name", in: "//dns.googleapis.com/projects/p/managedZones/my-zone", wantImport: "projects/real-proj/managedZones/my-zone"},
		{name: "asset name with numeric id resolves through CAI", in: numeric, searcher: true, wantImport: "projects/real-proj/managedZones/resolved-zone"},
		{name: "numeric id not found", in: "//dns.googleapis.com/projects/p/managedZones/555", searcher: true, wantErr: ErrNotFound},
		{name: "numeric id without searcher", in: numeric, wantErr: ErrNotSupported},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "numeric bare id", in: "12345", wantErr: ErrNotSupported},
		{name: "other service", in: "//pubsub.googleapis.com/projects/p/topics/t", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var searcher gcpAssetSearcher
			if tc.searcher {
				searcher = &fakeAssetSearcher{results: []gcpAssetResult{
					{Name: numeric, AssetType: dnsManagedZoneAssetType, DisplayName: "resolved-zone"},
				}}
			}
			got, err := newDNSManagedZoneDiscoverer().DiscoverByID(context.Background(), searcher, tc.in, "real-proj")
			if tc.wantErr != nil {
				require.True(t, errors.Is(err, tc.wantErr), "err=%v, want %v", err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantImport, got.Identity.ImportID)
			assert.Equal(t, dnsManagedZoneTFType, got.Identity.Type)
		})
	}
}

func TestIsDNSManagedZoneName(t *testing.T) {
	t.Parallel()
	assert.True(t, isDNSManagedZoneName("example-com"))
	assert.True(t, isDNSManagedZoneName("z1"))
	assert.False(t, isDNSManagedZoneName("4831990241185325831"))
	assert.False(t, isDNSManagedZoneName(""))
	assert.False(t, isDNSManagedZoneName("Upper"))
	assert.False(t, isDNSManagedZoneName("has.dot"))
}

// CAI's display name is free text from CAI's point of view. When it is
// not a valid zone name it cannot be the import ID, so FromAsset must
// fall back to the asset name segment, or skip the row when that
// segment is the numeric ID.
func TestDNSManagedZoneFromAsset_ValidatesDisplayName(t *testing.T) {
	t.Parallel()
	d := newDNSManagedZoneDiscoverer()

	fallback := d.FromAsset(addressBook{}, gcpAssetResult{
		Name:        "//dns.googleapis.com/projects/real-proj/managedZones/io-foo-zone",
		DisplayName: "Io Foo Zone",
	}, "real-proj")
	assert.Equal(t, "projects/real-proj/managedZones/io-foo-zone", fallback.Identity.ImportID)

	skipped := d.FromAsset(addressBook{}, gcpAssetResult{
		Name:        "//dns.googleapis.com/projects/real-proj/managedZones/4831990241185325831",
		DisplayName: "io-foo/zone",
	}, "real-proj")
	assert.Empty(t, skipped.Identity.Type)
}
