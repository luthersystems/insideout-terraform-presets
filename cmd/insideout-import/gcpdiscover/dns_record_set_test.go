package gcpdiscover

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeDNSRecordSetLister returns canned record sets per zone and an
// error for the zones listed in errByZone.
type fakeDNSRecordSetLister struct {
	mu        sync.Mutex
	byZone    map[string][]gcpDNSRecordSet
	errByZone map[string]error
	calls     []string // "<project>/<zone>"
}

func (f *fakeDNSRecordSetLister) ListDNSRecordSets(_ context.Context, projectID, zone string) ([]gcpDNSRecordSet, error) {
	f.mu.Lock()
	f.calls = append(f.calls, projectID+"/"+zone)
	f.mu.Unlock()
	if err := f.errByZone[zone]; err != nil {
		return nil, err
	}
	return f.byZone[zone], nil
}

func dnsZoneRow(name string) imported.ImportedResource {
	return imported.ImportedResource{Identity: imported.ResourceIdentity{
		Cloud:     "gcp",
		Type:      dnsManagedZoneTFType,
		NameHint:  name,
		ProjectID: "real-proj",
		ImportID:  dnsManagedZoneImportID("real-proj", name),
	}}
}

func TestDNSRecordSetListNonCAI_FansOutOverPriorZones(t *testing.T) {
	t.Parallel()
	fake := &fakeDNSRecordSetLister{byZone: map[string][]gcpDNSRecordSet{
		"zone-a": {{Name: "www.a.example.", Type: "A"}, {Name: "a.example.", Type: "TXT"}},
		"zone-b": {{Name: "api.b.example.", Type: "CNAME"}},
	}}
	d := newDNSRecordSetDiscoverer(fake).(*dnsRecordSetDiscoverer)
	prior := []imported.ImportedResource{
		dnsZoneRow("zone-a"),
		dnsZoneRow("zone-b"),
		// Not a zone: must not drive a fan-out.
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", NameHint: "zone-c"}},
	}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "io-foo", prior, progress.NopEmitter{})
	require.NoError(t, err)
	assert.Equal(t, []string{"real-proj/zone-a", "real-proj/zone-b"}, fake.calls)
	require.Len(t, got, 3)

	byImport := map[string]imported.ImportedResource{}
	for _, r := range got {
		byImport[r.Identity.ImportID] = r
	}
	www, ok := byImport["projects/real-proj/managedZones/zone-a/rrsets/www.a.example./A"]
	require.True(t, ok, "missing www A record; got %v", byImport)
	assert.Equal(t, dnsRecordSetTFType, www.Identity.Type)
	assert.Equal(t, "www.a.example-A", www.Identity.NameHint)
	assert.Equal(t, "real-proj", www.Identity.ProjectID)
	assert.Equal(t, "zone-a", www.Identity.NativeIDs["managed_zone"])
	assert.Equal(t, "www.a.example.", www.Identity.NativeIDs["record_name"])
	assert.Equal(t, "A", www.Identity.NativeIDs["record_type"])
	assert.Contains(t, byImport, "projects/real-proj/managedZones/zone-a/rrsets/a.example./TXT")
	assert.Contains(t, byImport, "projects/real-proj/managedZones/zone-b/rrsets/api.b.example./CNAME")
}

// One zone failing must not stop the others; the failure is surfaced as
// a ServiceWarn for the progress stream and a *PartialDiscoveryError
// for the caller, so a missing record set is not mistaken for a deleted
// one.
func TestDNSRecordSetListNonCAI_SoftFailsPerZone(t *testing.T) {
	t.Parallel()
	fake := &fakeDNSRecordSetLister{
		byZone:    map[string][]gcpDNSRecordSet{"good": {{Name: "x.example.", Type: "A"}}},
		errByZone: map[string]error{"bad": errors.New("permission denied")},
	}
	em := &recordingEmitter{}
	d := newDNSRecordSetDiscoverer(fake).(*dnsRecordSetDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", []imported.ImportedResource{dnsZoneRow("bad"), dnsZoneRow("good")}, em)
	var pe *PartialDiscoveryError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, dnsRecordSetTFType, pe.TFType)
	require.Len(t, pe.Errs, 1)
	assert.Contains(t, pe.Errs[0].Error(), `"bad"`)
	require.Len(t, got, 1)
	assert.Equal(t, "projects/real-proj/managedZones/good/rrsets/x.example./A", got[0].Identity.ImportID)
	warned := false
	for _, ev := range em.snapshot() {
		if ev.Kind == "service_warn" {
			warned = true
		}
	}
	assert.True(t, warned, "expected a service_warn for the failing zone")
}

func TestDNSRecordSetListNonCAI_NilListerIsNoop(t *testing.T) {
	t.Parallel()
	d := newDNSRecordSetDiscoverer(nil).(*dnsRecordSetDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", []imported.ImportedResource{dnsZoneRow("z")}, progress.NopEmitter{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// End to end: DiscoverTypes finds the zone through CAI, then the
// non-CAI phase lists that zone's record sets by zone NAME (taken from
// the CAI display name, not the numeric ID in the asset name).
func TestDNSRecordSet_DiscoverTypes_UsesCAIZoneName(t *testing.T) {
	t.Parallel()
	searcher := &fakeAssetSearcher{results: []gcpAssetResult{{
		Name:        "//dns.googleapis.com/projects/real-proj/managedZones/4831990241185325831",
		AssetType:   dnsManagedZoneAssetType,
		DisplayName: "io-foo-zone",
	}}}
	lister := &fakeDNSRecordSetLister{byZone: map[string][]gcpDNSRecordSet{
		"io-foo-zone": {{Name: "www.example.com.", Type: "A"}},
	}}
	g := NewGCPDiscoverer(searcher, "real-proj", GCPDiscovererOpts{DNSRecordSetLister: lister})
	got, err := g.DiscoverTypes(context.Background(), []string{dnsManagedZoneTFType, dnsRecordSetTFType}, DiscoverArgs{Project: "io-foo"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, []string{"real-proj/io-foo-zone"}, lister.calls)
	var imports []string
	for _, r := range got {
		imports = append(imports, r.Identity.ImportID)
	}
	assert.ElementsMatch(t, []string{
		"projects/real-proj/managedZones/io-foo-zone",
		"projects/real-proj/managedZones/io-foo-zone/rrsets/www.example.com./A",
	}, imports)
}

// A caller that asks only for record sets (ui-core's existence check,
// a compose run narrowed to the user's picks) still gets them:
// DiscoverTypes finds the zones as hidden parents and does not return
// the zone rows.
func TestDNSRecordSet_DiscoverTypes_OnlyRecordSetsRequested(t *testing.T) {
	t.Parallel()
	searcher := &fakeAssetSearcher{results: []gcpAssetResult{{
		Name:        "//dns.googleapis.com/projects/real-proj/managedZones/4831990241185325831",
		AssetType:   dnsManagedZoneAssetType,
		DisplayName: "io-foo-zone",
	}}}
	lister := &fakeDNSRecordSetLister{byZone: map[string][]gcpDNSRecordSet{
		"io-foo-zone": {{Name: "www.example.com.", Type: "A"}},
	}}
	em := &recordingEmitter{}
	var streamed []string
	g := NewGCPDiscoverer(searcher, "real-proj", GCPDiscovererOpts{DNSRecordSetLister: lister})
	got, err := g.DiscoverTypes(context.Background(), []string{dnsRecordSetTFType}, DiscoverArgs{
		Project: "io-foo",
		Emitter: em,
		OnTypeDiscovered: func(tfType string, _ []imported.ImportedResource) {
			streamed = append(streamed, tfType)
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 1, "only the record set comes back, not the zone")
	assert.Equal(t, dnsRecordSetTFType, got[0].Identity.Type)
	assert.Equal(t, "projects/real-proj/managedZones/io-foo-zone/rrsets/www.example.com./A", got[0].Identity.ImportID)
	assert.Equal(t, []string{"real-proj/io-foo-zone"}, lister.calls)

	// The zone was searched for, under the stack's label scope.
	require.Len(t, searcher.calls, 1)
	assert.Contains(t, searcher.calls[0].assetTypes, dnsManagedZoneAssetType)

	// The hidden zone produces no events the caller would see as results.
	assert.Equal(t, []string{dnsRecordSetTFType}, streamed)
	for _, ev := range em.snapshot() {
		if ev.Kind == "item_found" {
			assert.NotEqual(t, dnsManagedZoneTFType, ev.TFType, "hidden zone must not be reported as found")
		}
	}
}

// Through DiscoverTypes, a zone that fails to list returns the other
// zones' record sets together with a *PartialDiscoveryError, so a caller
// checking existence can tell "could not list" from "gone".
func TestDNSRecordSet_DiscoverTypes_PartialZoneFailureIsReturned(t *testing.T) {
	t.Parallel()
	searcher := &fakeAssetSearcher{results: []gcpAssetResult{
		{Name: "//dns.googleapis.com/projects/real-proj/managedZones/1", AssetType: dnsManagedZoneAssetType, DisplayName: "good"},
		{Name: "//dns.googleapis.com/projects/real-proj/managedZones/2", AssetType: dnsManagedZoneAssetType, DisplayName: "bad"},
	}}
	lister := &fakeDNSRecordSetLister{
		byZone:    map[string][]gcpDNSRecordSet{"good": {{Name: "x.example.", Type: "A"}}},
		errByZone: map[string]error{"bad": errors.New("Error 403: forbidden")},
	}
	g := NewGCPDiscoverer(searcher, "real-proj", GCPDiscovererOpts{DNSRecordSetLister: lister})
	got, err := g.DiscoverTypes(context.Background(), []string{dnsRecordSetTFType}, DiscoverArgs{Project: "io-foo"})
	var pe *PartialDiscoveryError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, dnsRecordSetTFType, pe.TFType)
	require.Len(t, got, 1)
	assert.Equal(t, "projects/real-proj/managedZones/good/rrsets/x.example./A", got[0].Identity.ImportID)
}

func TestNewDNSRecordSetListerFromService_NilServiceFails(t *testing.T) {
	t.Parallel()
	_, err := NewDNSRecordSetListerFromService(nil).ListDNSRecordSets(context.Background(), "p", "z")
	require.Error(t, err)
}

func TestDNSRecordSetDiscoverByID_NotSupported(t *testing.T) {
	t.Parallel()
	_, err := newDNSRecordSetDiscoverer(nil).DiscoverByID(context.Background(), nil, "anything", "p")
	assert.ErrorIs(t, err, ErrNotSupported)
}

func TestParseDNSRecordSetImportID(t *testing.T) {
	t.Parallel()
	p, z, n, typ := parseDNSRecordSetImportID("projects/real-proj/managedZones/zone-a/rrsets/www.a.example./A")
	assert.Equal(t, []string{"real-proj", "zone-a", "www.a.example.", "A"}, []string{p, z, n, typ})

	for _, bad := range []string{"", "projects/p/managedZones/z", "projects/p/topics/t/rrsets/n/A", "projects/p/managedZones/z/rrsets/n"} {
		p, z, n, typ := parseDNSRecordSetImportID(bad)
		assert.Empty(t, p+z+n+typ, "input %q", bad)
	}
}

// Cloud DNS creates the zone-apex SOA and NS record sets with the zone
// and deletes them with it. They must not be imported; an NS set below
// the apex (a delegation the user made) must be.
func TestDNSRecordSetListNonCAI_SkipsApexSOAAndNS(t *testing.T) {
	t.Parallel()
	fake := &fakeDNSRecordSetLister{byZone: map[string][]gcpDNSRecordSet{
		"zone-a": {
			{Name: "a.example.", Type: "NS"},
			{Name: "a.example.", Type: "SOA"},
			{Name: "a.example.", Type: "TXT"},
			{Name: "sub.a.example.", Type: "NS"},
			{Name: "www.a.example.", Type: "A"},
		},
	}}
	d := newDNSRecordSetDiscoverer(fake).(*dnsRecordSetDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "io-foo", []imported.ImportedResource{dnsZoneRow("zone-a")}, progress.NopEmitter{})
	require.NoError(t, err)

	var ids []string
	for _, r := range got {
		ids = append(ids, r.Identity.ImportID)
	}
	assert.ElementsMatch(t, []string{
		"projects/real-proj/managedZones/zone-a/rrsets/a.example./TXT",
		"projects/real-proj/managedZones/zone-a/rrsets/sub.a.example./NS",
		"projects/real-proj/managedZones/zone-a/rrsets/www.a.example./A",
	}, ids)
}
