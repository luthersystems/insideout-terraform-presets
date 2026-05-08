package gcpdiscover

import (
	"context"
	"errors"
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
	if _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{
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
			got, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
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
	got, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
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
	got, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
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
	_, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
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
	_, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{})
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
	if _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{Emitter: rec}); err != nil {
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
	if _, err := g.EnumerateUnsupported(context.Background(), UnsupportedArgs{
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
