package gcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// gcpAsset constructs a gcpAssetResult fixture with the fields
// EnumerateUnsupported reads.
func gcpAsset(name, assetType, location string, labels map[string]string) gcpAssetResult {
	return gcpAssetResult{
		Name:      name,
		AssetType: assetType,
		Location:  location,
		Labels:    labels,
	}
}

// TestEnumerateUnsupportedGCP_BuildsAssetTypesClauseFromUnsupportedSet
// pins the SearchAllResources request shape: the assetTypes filter
// covers exactly the keys of gcpUnsupportedTFTypeByAssetType minus any
// importable mappings (none currently overlap; the test asserts the
// invariant for future-proofing).
func TestEnumerateUnsupportedGCP_BuildsAssetTypesClauseFromUnsupportedSet(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	if _, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{
		Project: "io-foo",
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls=%d, want 1 (one SearchAllResources for unsupported)", len(fake.calls))
	}
	got := fake.calls[0].assetTypes
	// Must include every unimportable asset type from the lookup map.
	for assetType := range gcpUnsupportedTFTypeByAssetType {
		if !slices.Contains(got, assetType) {
			t.Errorf("assetTypes %v missing %s", got, assetType)
		}
	}
	// Must be sorted (deterministic on-the-wire shape).
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("assetTypes not sorted: %v", got)
			break
		}
	}
}

// TestEnumerateUnsupportedGCP_FiltersOutImportableTypes pins the
// registry-subtraction invariant: any importable mapping in the lookup
// is dropped from the SearchAllResources assetTypes filter. Today no
// entry overlaps, but a future change that adds an entry for a type
// the registry already imports would silently leak rows into
// unsupported.json — this test catches that.
func TestEnumerateUnsupportedGCP_FiltersOutImportableTypes(t *testing.T) {
	t.Parallel()
	// Construct a synthetic supported-set covering one of the lookup
	// entries to assert the subtract step actually fires.
	supportedSet := map[string]struct{}{
		"google_compute_instance": {},
	}
	got := gcpUnsupportedAssetTypes(supportedSet)
	for _, at := range got {
		if at == "compute.googleapis.com/Instance" {
			t.Errorf("assetTypes %v leaked %s despite being in supportedSet", got, at)
		}
	}
}

// TestEnumerateUnsupportedGCP_TFTypeMappedFromAssetType pins that each
// entry in the lookup map round-trips through EnumerateUnsupported to
// the right Terraform type. Mirrors the AWS test of the same shape.
func TestEnumerateUnsupportedGCP_TFTypeMappedFromAssetType(t *testing.T) {
	t.Parallel()
	for assetType, wantTF := range gcpUnsupportedTFTypeByAssetType {
		assetType, wantTF := assetType, wantTF
		t.Run(assetType, func(t *testing.T) {
			t.Parallel()
			fake := &fakeAssetSearcher{
				results: []gcpAssetResult{gcpAsset(
					"//"+strings.Split(assetType, "/")[0]+"/projects/p/things/foo",
					assetType,
					"us-central1",
					nil,
				)},
			}
			g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
			got, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d rows, want 1", len(got))
			}
			if got[0].Type != wantTF {
				t.Errorf("Type=%q, want %q for AssetType=%q", got[0].Type, wantTF, assetType)
			}
		})
	}
}

// TestEnumerateUnsupportedGCP_UnknownAssetTypePreservesEmpty pins the
// fall-through: an unknown asset type returned by the searcher
// produces a row with Type="" so the picker can still surface it.
func TestEnumerateUnsupportedGCP_UnknownAssetTypePreservesEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{gcpAsset(
			"//newservice.googleapis.com/projects/p/things/foo",
			"newservice.googleapis.com/Thing",
			"us-central1",
			nil,
		)},
	}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	got, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].Type != "" {
		t.Errorf("Type=%q, want empty for unknown AssetType", got[0].Type)
	}
	if got[0].Name != "foo" {
		t.Errorf("Name=%q, want %q (trailing path segment)", got[0].Name, "foo")
	}
}

// TestEnumerateUnsupportedGCP_LabelsPassThrough pins that asset.Labels
// flows into UnsupportedResource.Tags unchanged — the GCP path uses
// labels as the operator-facing tag concept, mirroring CLAUDE.md's
// labels-as-tags rule.
func TestEnumerateUnsupportedGCP_LabelsPassThrough(t *testing.T) {
	t.Parallel()
	wantLabels := map[string]string{"env": "prod", "owner": "team-a"}
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{gcpAsset(
			"//compute.googleapis.com/projects/p/zones/us/instances/vm",
			"compute.googleapis.com/Instance",
			"us",
			wantLabels,
		)},
	}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	got, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].Tags == nil || got[0].Tags["env"] != "prod" || got[0].Tags["owner"] != "team-a" {
		t.Errorf("Tags=%v, want %v passthrough", got[0].Tags, wantLabels)
	}
}

// TestEnumerateUnsupportedGCP_SearcherErrorIsReturned pins error
// propagation: a SearchAllResources failure surfaces through to the
// caller. The CLI's WARN-and-continue branch is exercised at the
// orchestrator level; here we just assert the wrap.
func TestEnumerateUnsupportedGCP_SearcherErrorIsReturned(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("permission denied: cloudasset.assets.searchAllResources")
	fake := &fakeAssetSearcher{err: wantErr}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	_, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
	if err == nil {
		t.Fatal("err=nil, want error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err=%v, want wrap of %v", err, wantErr)
	}
}

// TestEnumerateUnsupportedGCP_NilSearcherIsFatal pins the safety net.
func TestEnumerateUnsupportedGCP_NilSearcherIsFatal(t *testing.T) {
	t.Parallel()
	g := &GCPDiscoverer{projectID: "real-proj"}
	_, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
	if err == nil {
		t.Fatal("err=nil, want explicit error when no searcher configured")
	}
}

// TestEnumerateUnsupportedGCP_EmitsServiceStartFinish pins the
// progress-event contract: one (service_start, service_finish) bracket
// (one Search call covers all regions), plus item_found per row.
func TestEnumerateUnsupportedGCP_EmitsServiceStartFinish(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			gcpAsset("//compute.googleapis.com/projects/p/zones/us/instances/a", "compute.googleapis.com/Instance", "us", nil),
			gcpAsset("//compute.googleapis.com/projects/p/zones/us/instances/b", "compute.googleapis.com/Instance", "us", nil),
		},
	}
	rec := &recordingEmitter{}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	if _, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{Emitter: rec}); err != nil {
		t.Fatal(err)
	}
	starts, finishes, items := 0, 0, 0
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != "unsupported" {
				t.Errorf("service_start.service=%q, want unsupported", e.Service)
			}
			starts++
		case "service_finish":
			if e.Service != "unsupported" {
				t.Errorf("service_finish.service=%q, want unsupported", e.Service)
			}
			finishes++
		case "item_found":
			items++
		}
	}
	if starts != 1 {
		t.Errorf("service_start count=%d, want 1", starts)
	}
	if finishes != 1 {
		t.Errorf("service_finish count=%d, want 1", finishes)
	}
	if items != 2 {
		t.Errorf("item_found count=%d, want 2", items)
	}
}

// TestEnumerateUnsupportedGCP_QueryShapeFromArgs pins the search-query
// composition: --regions and --tag-selectors flow through buildSearchQuery.
func TestEnumerateUnsupportedGCP_QueryShapeFromArgs(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	if _, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{
		Project:      "io-foo",
		Regions:      []string{"us-central1"},
		TagSelectors: []TagSelector{{Key: "env", Value: "prod"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls=%d, want 1", len(fake.calls))
	}
	q := fake.calls[0].query
	if !strings.Contains(q, "labels.project:io-foo") {
		t.Errorf("query=%q missing labels.project clause", q)
	}
	if !strings.Contains(q, "location:us-central1") {
		t.Errorf("query=%q missing location clause", q)
	}
	if !strings.Contains(q, "labels.env:prod") {
		t.Errorf("query=%q missing tag-selector clause", q)
	}
}

// TestEnumerateUnsupportedGCP_PopulatesGroup pins the (#297) Category
// wire-through for the GCP path: every emitted UnsupportedResource
// carries a non-empty Group when its Type is in the categorized set,
// and an empty Group for unmapped Cloud Asset slugs.
//
// Mirrors TestEnumerateUnsupported_PopulatesGroup on the AWS side. A
// regression that wired the wrong category map (or forgot to call
// imported.Category) surfaces here.
func TestEnumerateUnsupportedGCP_PopulatesGroup(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			// Cover one row per still-unsupported category. Bundle 8
			// continues to move types from unsupported → supported, so
			// pin against types that are NOT in the live registry: as
			// of #370 that includes compute_disk (Data Storage),
			// compute_subnetwork (Network Security). Once Bundle 8
			// PR 6 (#371) ships container_cluster will also move and
			// this fixture will need re-targeting.
			gcpAsset("//compute.googleapis.com/projects/p/zones/us-central1-a/disks/d", "compute.googleapis.com/Disk", "us-central1", nil),
			gcpAsset("//compute.googleapis.com/projects/p/regions/us-central1/subnetworks/s", "compute.googleapis.com/Subnetwork", "us-central1", nil),
			// Unmapped slug — must pass through with empty Type and Group.
			gcpAsset("//newservice.googleapis.com/projects/p/things/x", "newservice.googleapis.com/Thing", "us-central1", nil),
		},
	}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	got, _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
	if err != nil {
		t.Fatal(err)
	}
	// Walk the slice directly: the previous map-keyed assertion folded
	// every Type=="" row onto a single key, hiding any case where two
	// unmapped slugs collided. Find each expected row by Type and
	// assert Group on the matching entry; for the unmapped row, walk
	// for the Type=="" entry and assert Group=="".
	wantGroup := map[string]string{
		"google_compute_disk":       "Data Storage",
		"google_compute_subnetwork": "Network Security",
	}
	for typ, want := range wantGroup {
		var found *UnsupportedResource
		for i := range got {
			if got[i].Type == typ {
				found = &got[i]
				break
			}
		}
		if found == nil {
			t.Errorf("type %q not found in emitted rows %+v", typ, got)
			continue
		}
		if found.Group != want {
			t.Errorf("Group for %q = %q, want %q", typ, found.Group, want)
		}
	}
	// Unmapped slug: there must be a row with Type=="" whose Group is
	// also "" (Category("") returns ""). Asserting on the slice
	// directly defends against the previous map-keyed shape, which
	// silently passed if the Type=="" row was missing entirely.
	var unmapped *UnsupportedResource
	for i := range got {
		if got[i].Type == "" {
			unmapped = &got[i]
			break
		}
	}
	if unmapped == nil {
		t.Fatalf("no row with Type==\"\" emitted; unmapped Cloud Asset slug must still surface (rows=%+v)", got)
	}
	if unmapped.Group != "" {
		t.Errorf("Group for Type==\"\" = %q, want \"\" (unmapped slug → no category)", unmapped.Group)
	}
}

// --- #309 MaxResults cap tests ---

// TestEnumerateUnsupportedGCP_CapFiresAndSetsTruncated pins the
// wrapper-level cap (#309): a 50-asset fake response with cap=10
// returns exactly 10 rows and truncated=true.
//
// IMPORTANT: unlike the AWS path, the GCP cap is at the
// EnumerateUnsupported wrapper, not inside SearchAll. SearchAll is
// shared between the importable and unsupported scans; capping there
// would silently truncate the importable manifest. The trade-off is
// that the cap bounds memory + on-disk size, but does NOT bound the
// Cloud Asset API budget — SearchAll still walks the full iterator.
func TestEnumerateUnsupportedGCP_CapFiresAndSetsTruncated(t *testing.T) {
	t.Parallel()
	results := make([]gcpAssetResult, 0, 50)
	for i := 0; i < 50; i++ {
		// compute.googleapis.com/Instance maps to google_compute_instance
		// which is currently NOT in the GCP importable registry, so
		// each fixture row passes through to the output.
		name := fmt.Sprintf("//compute.googleapis.com/projects/p/zones/us/instances/vm-%03d", i)
		results = append(results, gcpAsset(name, "compute.googleapis.com/Instance", "us", nil))
	}
	fake := &fakeAssetSearcher{results: results}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	got, truncated, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{
		MaxResults: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Errorf("truncated=false, want true (cap=10, source=50)")
	}
	if len(got) != 10 {
		t.Errorf("len(got)=%d, want 10 (cap)", len(got))
	}
	// Wrapper-level cap means SearchAll DID see all 50 results — it
	// emits one call as usual; the truncation happens after the
	// iterator returns. Pin the call count to defend against a
	// future regression that "optimizes" the cap into SearchAll.
	if len(fake.calls) != 1 {
		t.Errorf("SearchAll calls=%d, want 1 (cap is wrapper-level, not per-call)", len(fake.calls))
	}
}

// TestEnumerateUnsupportedGCP_CapZeroDisablesLimit pins the
// "0 = unbounded" contract on the GCP path. Mirrors the AWS-side test.
func TestEnumerateUnsupportedGCP_CapZeroDisablesLimit(t *testing.T) {
	t.Parallel()
	results := make([]gcpAssetResult, 0, 50)
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("//compute.googleapis.com/projects/p/zones/us/instances/vm-%03d", i)
		results = append(results, gcpAsset(name, "compute.googleapis.com/Instance", "us", nil))
	}
	fake := &fakeAssetSearcher{results: results}
	g := &GCPDiscoverer{searcher: fake, projectID: "real-proj"}
	got, truncated, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{
		MaxResults: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Errorf("truncated=true, want false (cap=0 disables the limit)")
	}
	if len(got) != 50 {
		t.Errorf("len(got)=%d, want 50 (uncapped)", len(got))
	}
}

// TestGCPResourceNameFromAssetName_TrailingSegment pins the display-
// name extraction across asset-name shapes Cloud Asset hands back.
func TestGCPResourceNameFromAssetName_TrailingSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want string
	}{
		{"//compute.googleapis.com/projects/p/zones/us-central1-a/instances/my-vm", "my-vm"},
		{"//bigquery.googleapis.com/projects/p/datasets/my_ds", "my_ds"},
		{"//container.googleapis.com/projects/p/locations/us-central1/clusters/c", "c"},
		{"", ""},
	}
	for _, tc := range cases {
		got := gcpResourceNameFromAssetName(tc.name)
		if got != tc.want {
			t.Errorf("gcpResourceNameFromAssetName(%q)=%q, want %q", tc.name, got, tc.want)
		}
	}
}
