package gcpdiscover

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// #777 selection-closure scoping (GCP). These tests pin the parent-scope
// seam that restricts CAI enumeration to the operator's selected parents,
// removing the project-wide `labels.project:<stack>` SearchAllResources
// sweep (and the broad Cloud Asset Inventory read scope it requires). It is
// the GCP twin of awsdiscover/closure_scope_test.go.

// TestNewParentScope_DedupesSortsAndDropsEmpties pins the scope
// constructor's normalization: per-asset-type de-dup by (name, location),
// sort, empty drop, and a nil result when no usable pair survives.
func TestNewParentScope_DedupesSortsAndDropsEmpties(t *testing.T) {
	t.Parallel()
	scope := NewParentScope(map[string][]ScopedParent{
		"storage.googleapis.com/Bucket": {
			{Name: "b-2", Location: "us"},
			{Name: "b-1", Location: "us"},
			{Name: "b-2", Location: "us"},  // dup (name,location) dropped
			{Name: "  ", Location: "us"},   // empty name dropped
			{Name: "b-1", Location: "us"},  // dup dropped
			{Name: "b-1", Location: "eu"},  // same name, different location: kept
			{Name: " b-3 ", Location: " "}, // trimmed
		},
		"  ":                           {{Name: "ignored"}},         // empty asset type dropped
		"compute.googleapis.com/Empty": {{Name: ""}, {Name: "   "}}, // no usable names dropped
	})
	wantBuckets := []ScopedParent{
		{Name: "b-1", Location: "eu"},
		{Name: "b-1", Location: "us"},
		{Name: "b-2", Location: "us"},
		{Name: "b-3", Location: ""},
	}
	if got := scope["storage.googleapis.com/Bucket"]; !reflect.DeepEqual(got, wantBuckets) {
		t.Errorf("bucket scope = %v, want sorted de-duped by (name,location) %v", got, wantBuckets)
	}
	if _, ok := scope["  "]; ok {
		t.Error("empty asset type should be dropped")
	}
	if _, ok := scope["compute.googleapis.com/Empty"]; ok {
		t.Error("asset type with no usable names should be dropped")
	}
	if NewParentScope(map[string][]ScopedParent{"x": {{Name: ""}}}) != nil {
		t.Error("scope with no usable pair should be nil")
	}
}

// TestScopedParentNames pins the accessor: a scoped asset type returns its
// names + true; an unscoped one returns (nil, false); a nil scope returns
// (nil, false).
func TestScopedParentNames(t *testing.T) {
	t.Parallel()
	args := DiscoverArgs{ParentScope: NewParentScope(map[string][]ScopedParent{
		"storage.googleapis.com/Bucket": {{Name: "b-a"}, {Name: "b-b"}},
	})}
	names, ok := args.scopedParentNames("storage.googleapis.com/Bucket")
	if !ok {
		t.Fatal("scoped asset type should report ok=true")
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"b-a", "b-b"}) {
		t.Errorf("names = %v, want [b-a b-b]", names)
	}
	if _, ok := args.scopedParentNames("pubsub.googleapis.com/Topic"); ok {
		t.Error("unscoped asset type should report ok=false")
	}
	if _, ok := (DiscoverArgs{}).scopedParentNames("storage.googleapis.com/Bucket"); ok {
		t.Error("nil ParentScope should report ok=false")
	}
}

// TestDiscoverTypes_ParentScopeNarrowsToSelectedParents proves the #777
// scoping: with a ParentScope restricting google_storage_bucket to the
// selected bucket names, DiscoverTypes issues a SINGLE SearchAll for the
// bucket asset type whose query is the per-parent `name:(...)` clause (NOT
// the project-wide labels.project sweep), and never sweeps the bucket type
// account-wide. Mirror of
// TestCloudControlDiscover_ParentScopeSkipsListResources.
func TestDiscoverTypes_ParentScopeNarrowsToSelectedParents(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{Name: "//storage.googleapis.com/buckets/io-uploads", AssetType: storageBucketAssetType, Location: "us"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	got, err := g.DiscoverTypes(context.Background(), []string{"google_storage_bucket"}, DiscoverArgs{
		Project: "io-foo",
		Regions: []string{"us"},
		ParentScope: NewParentScope(map[string][]ScopedParent{
			storageBucketAssetType: {{Name: "io-uploads", Location: "us"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll called %d times, want 1 (one scoped call)", len(fake.calls))
	}
	c := fake.calls[0]
	if len(c.assetTypes) != 1 || c.assetTypes[0] != storageBucketAssetType {
		t.Errorf("assetTypes=%v, want [%s]", c.assetTypes, storageBucketAssetType)
	}
	// The scoped query narrows by the selected parent's name and must NOT
	// carry the project-wide labels.project sweep clause — the operator
	// selected the bucket by identity (scope bypasses the project filter,
	// mirror of TestCloudControlDiscover_ParentScopeBypassesTagFilter).
	if !strings.Contains(c.query, "name:io-uploads") {
		t.Errorf("query=%q, want it to narrow by name:io-uploads", c.query)
	}
	if strings.Contains(c.query, "labels.project:") {
		t.Errorf("query=%q, want NO project-wide labels.project sweep clause for a scoped type", c.query)
	}
	if !strings.Contains(c.query, "location:us") {
		t.Errorf("query=%q, want location:us to still propagate", c.query)
	}
	if len(got) != 1 || got[0].Identity.NameHint != "io-uploads" {
		t.Errorf("discovered = %v, want exactly the scoped bucket io-uploads", got)
	}
}

// TestDiscoverTypes_ParentScopeExactFiltersSubstringCollisions proves the
// client-side exact filter (filterToSelectedParents) defends against the
// substring nature of the CAI `name:` operator: a `name:io-uploads` query can
// still return "io-uploads-backup" / "my-io-uploads" of the same asset type,
// and because the scoped query drops the labels.project clause those rows are
// not otherwise excluded. Only the exactly-named parent must survive into the
// closure — otherwise unselected parents leak and fan out child discovery.
// Regression guard for the #777 codex-review finding.
func TestDiscoverTypes_ParentScopeExactFiltersSubstringCollisions(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		// CAI `name:io-uploads` is a substring match, so the server returns
		// the selected bucket AND its substring neighbors.
		results: []gcpAssetResult{
			{Name: "//storage.googleapis.com/buckets/io-uploads-backup", AssetType: storageBucketAssetType, Location: "us"},
			{Name: "//storage.googleapis.com/buckets/io-uploads", AssetType: storageBucketAssetType, Location: "us"},
			{Name: "//storage.googleapis.com/buckets/my-io-uploads", AssetType: storageBucketAssetType, Location: "us"},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	got, err := g.DiscoverTypes(context.Background(), []string{"google_storage_bucket"}, DiscoverArgs{
		Project: "io-foo",
		Regions: []string{"us"},
		ParentScope: NewParentScope(map[string][]ScopedParent{
			storageBucketAssetType: {{Name: "io-uploads", Location: "us"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("discovered %d resources, want exactly 1 (substring neighbors must be filtered out): %v", len(got), got)
	}
	if got[0].Identity.NameHint != "io-uploads" {
		t.Errorf("discovered = %v, want exactly the exact-named bucket io-uploads (not a substring neighbor)", got)
	}
}

// TestDiscoverTypes_ParentScopeMultipleParentsOredByName proves a closure
// with two selected buckets ORs both names into one `name:(...)` clause —
// each parent's children are enumerated, neither account-wide.
func TestDiscoverTypes_ParentScopeMultipleParentsOredByName(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	_, err := g.DiscoverTypes(context.Background(), []string{"google_storage_bucket"}, DiscoverArgs{
		Project: "io-foo",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			storageBucketAssetType: {{Name: "io-uploads"}, {Name: "io-logs"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll calls=%d, want 1", len(fake.calls))
	}
	q := fake.calls[0].query
	if !strings.Contains(q, "name:io-uploads") || !strings.Contains(q, "name:io-logs") {
		t.Errorf("query=%q, want both selected bucket names", q)
	}
	if !strings.Contains(q, " OR ") {
		t.Errorf("query=%q, want the two names OR-combined", q)
	}
	if strings.Contains(q, "labels.project:") {
		t.Errorf("query=%q, want NO project-wide labels.project sweep clause for a scoped type", q)
	}
}

// TestDiscoverTypes_ScopedTypeNoParentSkipsEnumeration proves the GCP
// (empty, true) skip contract: an asset type that the scope OWNS but has
// ZERO selected parents for must SKIP enumeration entirely — no scoped
// call AND no project-wide fallback sweep. Mirror of
// TestCloudControlDiscover_ScopedTypeNoParentInRegionSkipsSweep.
func TestDiscoverTypes_ScopedTypeNoParentSkipsEnumeration(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	// Scope OWNS the bucket asset type but with ZERO usable parents would
	// be impossible via NewParentScope (it drops empty buckets), so we
	// construct the (empty, true) skip case directly: a ParentScope map
	// whose bucket entry is empty. searchScopedAssetTypes must skip it.
	args := DiscoverArgs{
		Project: "io-foo",
		// Hand-built ParentScope (NOT via NewParentScope) so the bucket
		// asset type is PRESENT with an empty parent slice — the
		// scoped-but-no-parent state DiscoverTypes must skip rather than
		// sweep project-wide.
		ParentScope: ParentScope{storageBucketAssetType: {}},
	}
	_, err := g.DiscoverTypes(context.Background(), []string{"google_storage_bucket"}, args)
	if err != nil {
		t.Fatal(err)
	}
	// The bucket type is scoped-but-empty ⇒ skipped: zero SearchAll calls
	// (no project-wide sweep, no scoped call).
	if len(fake.calls) != 0 {
		t.Fatalf("SearchAll calls=%d, want 0 (scoped-but-no-parent type must skip enumeration, not sweep)", len(fake.calls))
	}
}

// TestDiscoverTypes_ScopedAndUnscopedTypesCoexist proves the partition is
// CONSUMED correctly by searchBuckets: a closure that scopes ONE requested
// type (google_storage_bucket) while ALSO requesting an UNSCOPED type
// (google_pubsub_topic) must issue exactly TWO SearchAll calls — one scoped
// `name:(...)` call for the bucket and one project-wide `labels.project`
// sweep for the topic — never sweeping the scoped bucket type project-wide.
//
// This is the regression guard for a partition-discard bug in searchBuckets
// (e.g. keeping the original bucket instead of the unscoped remainder): with
// only single-type scoped tests, such a bug stays green because the unscoped
// remainder is empty and the swept call is skipped. Here the remainder is
// non-empty, so a discarded split surfaces as a swept bucket-type call.
func TestDiscoverTypes_ScopedAndUnscopedTypesCoexist(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})

	_, err := g.DiscoverTypes(context.Background(), []string{"google_storage_bucket", "google_pubsub_topic"}, DiscoverArgs{
		Project: "io-foo",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			storageBucketAssetType: {{Name: "io-uploads"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("SearchAll calls=%d, want 2 (one scoped bucket call + one swept topic call)", len(fake.calls))
	}

	var scopedCall, sweptCall *searchAllCall
	for i := range fake.calls {
		switch fake.calls[i].assetTypes[0] {
		case storageBucketAssetType:
			scopedCall = &fake.calls[i]
		case "pubsub.googleapis.com/Topic":
			sweptCall = &fake.calls[i]
		}
	}
	if scopedCall == nil || sweptCall == nil {
		t.Fatalf("missing a call: scoped=%v swept=%v (calls=%+v)", scopedCall, sweptCall, fake.calls)
	}
	// Scoped bucket call: per-parent name filter, NO project-wide sweep.
	if !strings.Contains(scopedCall.query, "name:io-uploads") {
		t.Errorf("scoped call query=%q, want name:io-uploads", scopedCall.query)
	}
	if strings.Contains(scopedCall.query, "labels.project:") {
		t.Errorf("scoped call query=%q, want NO project-wide sweep — the bucket type must not also be swept", scopedCall.query)
	}
	// Unscoped topic call: project-wide sweep stays intact.
	if !strings.Contains(sweptCall.query, "labels.project:io-foo") {
		t.Errorf("swept call query=%q, want the project-wide labels.project sweep for the unscoped type", sweptCall.query)
	}
	if strings.Contains(sweptCall.query, "name:") {
		t.Errorf("swept call query=%q, want NO per-parent name clause on the unscoped type", sweptCall.query)
	}
}

// TestDiscoverTypes_ScopedAcrossCAIScopeStyles proves the scoped split fires
// for every CAI ScopeStyle bucket — labels, name-prefix, and
// parent-name-prefix — not just the labels bucket. searchBuckets calls
// splitScopedBucket on all three CAI buckets; deleting any branch must fail
// here. Uses the fake discoverers so a label-less scoped parent (e.g. a KMS
// keyring, ScopeStyleNamePrefix) is exercised without registering a real
// type.
func TestDiscoverTypes_ScopedAcrossCAIScopeStyles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		assetType string
		disco     Discoverer
	}{
		{"labels", storageBucketAssetType, newStorageBucketDiscoverer()},
		{"name-prefix", "test.googleapis.com/NamePrefixed", &fakeNamePrefixDiscoverer{resourceType: "google_test_nameprefixed", assetType: "test.googleapis.com/NamePrefixed"}},
		{"parent-name-prefix", "test.googleapis.com/ParentScoped", &fakeParentNamePrefixDiscoverer{resourceType: "google_test_parentscoped", assetType: "test.googleapis.com/ParentScoped", parentMarker: "/parents/"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeAssetSearcher{}
			g := &GCPDiscoverer{
				searcher:  fake,
				projectID: "real-proj",
				byType:    map[string]Discoverer{tc.disco.ResourceType(): tc.disco},
			}
			_, err := g.DiscoverTypes(context.Background(), []string{tc.disco.ResourceType()}, DiscoverArgs{
				Project: "io-foo",
				ParentScope: NewParentScope(map[string][]ScopedParent{
					tc.assetType: {{Name: "io-selected"}},
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) != 1 {
				t.Fatalf("SearchAll calls=%d, want 1 (one scoped call for the %s bucket)", len(fake.calls), tc.name)
			}
			q := fake.calls[0].query
			if !strings.Contains(q, "name:io-selected") {
				t.Errorf("%s scoped query=%q, want name:io-selected (scope must apply to this bucket too)", tc.name, q)
			}
			if strings.Contains(q, "labels.project:") {
				t.Errorf("%s scoped query=%q, want NO project-wide sweep", tc.name, q)
			}
		})
	}
}

// TestSplitScopedBucket pins the partition helper: discoverers whose asset
// type is in scope are peeled out (their asset type returned, de-duped and
// sorted); the rest stay in the swept bucket.
func TestSplitScopedBucket(t *testing.T) {
	t.Parallel()
	bucket := []Discoverer{
		newStorageBucketDiscoverer(),       // storage.googleapis.com/Bucket
		newPubsubTopicDiscoverer(),         // pubsub.googleapis.com/Topic
		newSecretManagerSecretDiscoverer(), // secretmanager.googleapis.com/Secret
	}
	ps := ParentScope{
		storageBucketAssetType: {{Name: "io-uploads"}},
	}
	unscoped, scopedTypes := splitScopedBucket(bucket, ps)
	if !reflect.DeepEqual(scopedTypes, []string{storageBucketAssetType}) {
		t.Errorf("scopedTypes=%v, want [%s]", scopedTypes, storageBucketAssetType)
	}
	gotUnscoped := make([]string, 0, len(unscoped))
	for _, d := range unscoped {
		gotUnscoped = append(gotUnscoped, d.AssetType())
	}
	sort.Strings(gotUnscoped)
	want := []string{"pubsub.googleapis.com/Topic", "secretmanager.googleapis.com/Secret"}
	if !reflect.DeepEqual(gotUnscoped, want) {
		t.Errorf("unscoped asset types=%v, want %v", gotUnscoped, want)
	}
}

// TestBuildScopedSearchQuery pins the per-parent query shape: a single name
// emits a bare `name:` clause, multiple names OR inside parens, locations
// AND on, and the project-wide labels.project clause is NEVER present.
func TestBuildScopedSearchQuery(t *testing.T) {
	t.Parallel()
	if got := buildScopedSearchQuery([]string{"b-a"}, nil, nil); got != "name:b-a" {
		t.Errorf("single-name query=%q, want name:b-a", got)
	}
	got := buildScopedSearchQuery([]string{"b-a", "b-b"}, []string{"us"}, nil)
	if !strings.Contains(got, "(name:b-a OR name:b-b)") {
		t.Errorf("multi-name query=%q, want OR-combined names in parens", got)
	}
	if !strings.Contains(got, "location:us") {
		t.Errorf("query=%q, want location:us", got)
	}
	if strings.Contains(got, "labels.project") {
		t.Errorf("query=%q, scoped query must never carry labels.project", got)
	}
}
