package awsdiscover

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// #739 selection-closure scoping. These tests pin the parent-scope seam that
// restricts child + parent re-discovery to the operator's selected parents,
// removing the account-wide ListResources / ListParents enumeration (and the
// account-wide list permissions it requires).

// TestNewParentScope_DedupesSortsAndDropsEmpties pins the scope constructor's
// normalization: per-type de-dup, sort, empty drop, and a nil result when no
// usable pair survives.
func TestNewParentScope_DedupesSortsAndDropsEmpties(t *testing.T) {
	t.Parallel()
	scope := NewParentScope(map[string][]ScopedParent{
		"AWS::S3::Bucket": {
			{Identifier: "b-2", Region: "us-east-1"},
			{Identifier: "b-1", Region: "us-east-1"},
			{Identifier: "b-2", Region: "us-east-1"}, // dup (id,region) dropped
			{Identifier: "  ", Region: "us-east-1"},  // empty id dropped
			{Identifier: "b-1", Region: "us-east-1"}, // dup dropped
			// Same identifier, different region: NOT a dup — kept separately.
			{Identifier: "b-1", Region: "eu-west-1"},
		},
		"AWS::Logs::LogGroup": {
			{Identifier: " lg ", Region: " us-east-1 "}, // trimmed
			{Identifier: "lg", Region: "us-east-1"},     // dup after trim dropped
		},
		"  ":               {{Identifier: "ignored"}},               // empty CFN type dropped
		"AWS::Empty::Type": {{Identifier: ""}, {Identifier: "   "}}, // no usable ids dropped
	})
	wantS3 := []ScopedParent{
		{Identifier: "b-1", Region: "eu-west-1"},
		{Identifier: "b-1", Region: "us-east-1"},
		{Identifier: "b-2", Region: "us-east-1"},
	}
	if got := scope["AWS::S3::Bucket"]; !reflect.DeepEqual(got, wantS3) {
		t.Errorf("S3 scope = %v, want sorted de-duped by (id,region) %v", got, wantS3)
	}
	if got := scope["AWS::Logs::LogGroup"]; !reflect.DeepEqual(got, []ScopedParent{{Identifier: "lg", Region: "us-east-1"}}) {
		t.Errorf("Logs scope = %v, want trimmed de-duped [{lg us-east-1}]", got)
	}
	if _, ok := scope["  "]; ok {
		t.Error("empty CFN type should be dropped")
	}
	if _, ok := scope["AWS::Empty::Type"]; ok {
		t.Error("type with no usable ids should be dropped")
	}
	if NewParentScope(map[string][]ScopedParent{"x": {{Identifier: ""}}}) != nil {
		t.Error("scope with no usable pair should be nil")
	}
}

// TestCloudControlDiscover_ParentScopeSkipsListResources proves the parent-type
// scope short-circuit: when ParentScope restricts this type's CloudFormation
// type, the discoverer GetResources EXACTLY the scoped identifiers and issues
// ZERO ListResources calls — eliminating the account-wide list (and its
// permission). Pre-#739 it always swept ListResources.
func TestCloudControlDiscover_ParentScopeSkipsListResources(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		// A full account sweep would surface bucket-a..c; the scope must
		// keep us from ever listing them.
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "bucket-a", "bucket-b", "bucket-c", "bucket-unselected"),
		},
		propsByIdentifier: map[string]map[string]any{
			"bucket-a":          {"Tags": map[string]any{}},
			"bucket-b":          {"Tags": map[string]any{}},
			"bucket-c":          {"Tags": map[string]any{}},
			"bucket-unselected": {"Tags": map[string]any{}},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::S3::Bucket"
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:     []string{"us-east-1"},
		AccountID:   "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{"AWS::S3::Bucket": {{Identifier: "bucket-a", Region: "us-east-1"}, {Identifier: "bucket-b", Region: "us-east-1"}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListResources calls = %d, want 0 (scope must skip the account-wide list)", fake.listCalls)
	}
	gotIDs := importIDs(got)
	if !reflect.DeepEqual(gotIDs, []string{"bucket-a", "bucket-b"}) {
		t.Errorf("discovered = %v, want exactly the scoped parents [bucket-a bucket-b]", gotIDs)
	}
	sort.Strings(fake.getResourceCalls)
	if !reflect.DeepEqual(fake.getResourceCalls, []string{"bucket-a", "bucket-b"}) {
		t.Errorf("GetResource calls = %v, want only the scoped parents", fake.getResourceCalls)
	}
}

// TestCloudControlDiscover_IdentifierSharedChildScoped proves the codex #770
// follow-up: aws_s3_bucket_policy (CFN AWS::S3::BucketPolicy), whose CC
// identifier IS the bucket name, is scoped by the selected bucket names too —
// it GetResources only the selected buckets' policies and never lists the
// AWS::S3::BucketPolicy type account-wide.
func TestCloudControlDiscover_IdentifierSharedChildScoped(t *testing.T) {
	t.Parallel()
	if got := IdentifierSharedChildCFNTypes("AWS::S3::Bucket"); !reflect.DeepEqual(got, []string{"AWS::S3::BucketPolicy"}) {
		t.Fatalf("IdentifierSharedChildCFNTypes(AWS::S3::Bucket) = %v, want [AWS::S3::BucketPolicy]", got)
	}
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "bucket-a", "bucket-b", "bucket-unselected"),
		},
		propsByIdentifier: map[string]map[string]any{
			"bucket-a":          {},
			"bucket-unselected": {},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::S3::BucketPolicy"
	cfg.SkipProjectTagFilter = true
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:     []string{"us-east-1"},
		AccountID:   "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{"AWS::S3::BucketPolicy": {{Identifier: "bucket-a", Region: "us-east-1"}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListResources calls = %d, want 0 (scoped child must skip the account-wide list)", fake.listCalls)
	}
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"bucket-a"}) {
		t.Errorf("discovered = %v, want only the selected bucket's policy [bucket-a]", ids)
	}
}

// TestCloudControlDiscover_ParentScopeBypassesTagFilter proves a scoped parent
// is NOT dropped by the Project tag filter — the operator selected it by
// identity, so a missing Project tag must not exclude it.
func TestCloudControlDiscover_ParentScopeBypassesTagFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		propsByIdentifier: map[string]map[string]any{
			// No Project tag — the account-wide path would drop this under
			// args.Project filtering.
			"bucket-a": {"Tags": map[string]any{}},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::S3::Bucket"
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:     []string{"us-east-1"},
		AccountID:   "123",
		Project:     "io-some-project",
		ParentScope: NewParentScope(map[string][]ScopedParent{"AWS::S3::Bucket": {{Identifier: "bucket-a", Region: "us-east-1"}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"bucket-a"}) {
		t.Errorf("discovered = %v, want the scoped parent kept despite the Project filter", ids)
	}
}

// TestSDKOnlySubresourceDiscover_ParentScopeSkipsListParents proves the SDK-only
// sub-resource scope: when ParentScope restricts the parent CFN type, the
// per-parent FetchItem fan-out runs against the SCOPED parents and ListParents
// (the account-wide s3:ListBuckets) is never called.
func TestSDKOnlySubresourceDiscover_ParentScopeSkipsListParents(t *testing.T) {
	t.Parallel()
	var listParentsCalls atomic.Int64
	outcomes := map[string]fakeFetchOutcome{
		"bucket-a": {exists: true, props: map[string]any{}, nativeIDs: map[string]string{"bucket": "bucket-a"}},
		"bucket-b": {exists: true, props: map[string]any{}, nativeIDs: map[string]string{"bucket": "bucket-b"}},
		"bucket-c": {exists: true, props: map[string]any{}, nativeIDs: map[string]string{"bucket": "bucket-c"}},
	}
	cfg := sdkOnlyTestConfig()
	cfg.ParentCFNType = "AWS::S3::Bucket"
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		listParentsCalls.Add(1)
		return []string{"bucket-a", "bucket-b", "bucket-c"}, nil
	}
	var fetchCalls atomic.Int64
	cfg.FetchItem = fakeFetchItem(outcomes, &fetchCalls)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:     []string{"us-east-1"},
		AccountID:   "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{"AWS::S3::Bucket": {{Identifier: "bucket-a", Region: "us-east-1"}, {Identifier: "bucket-b", Region: "us-east-1"}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if listParentsCalls.Load() != 0 {
		t.Errorf("ListParents calls = %d, want 0 (scope must skip the account-wide parent list)", listParentsCalls.Load())
	}
	if fetchCalls.Load() != 2 {
		t.Errorf("FetchItem calls = %d, want 2 (only the scoped parents)", fetchCalls.Load())
	}
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"bucket-a", "bucket-b"}) {
		t.Errorf("discovered = %v, want only the scoped parents", ids)
	}
}

// TestSDKOnlySubresourceDiscover_NoScopeSweepsAllParents pins the back-compat
// path: an absent ParentScope leaves the account-wide ListParents sweep intact
// (top-level discovery + the local CLI's full scan keep working).
func TestSDKOnlySubresourceDiscover_NoScopeSweepsAllParents(t *testing.T) {
	t.Parallel()
	var listParentsCalls atomic.Int64
	outcomes := map[string]fakeFetchOutcome{
		"bucket-a": {exists: true, nativeIDs: map[string]string{"bucket": "bucket-a"}},
		"bucket-b": {exists: true, nativeIDs: map[string]string{"bucket": "bucket-b"}},
	}
	cfg := sdkOnlyTestConfig()
	cfg.ParentCFNType = "AWS::S3::Bucket"
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		listParentsCalls.Add(1)
		return []string{"bucket-a", "bucket-b"}, nil
	}
	cfg.FetchItem = fakeFetchItem(outcomes, nil)

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if listParentsCalls.Load() != 1 {
		t.Errorf("ListParents calls = %d, want 1 (no scope ⇒ account-wide sweep)", listParentsCalls.Load())
	}
	if len(got) != 2 {
		t.Errorf("discovered %d, want 2 (full sweep)", len(got))
	}
}

// TestLogGroupParentLister_ParentScopeSkipsDescribe proves the CloudWatch Logs
// parent lister honors the scope: with a ParentScope it wraps the scoped log
// group names directly into resource models and never calls the account-wide
// logs:DescribeLogGroups. A nil client would panic if Describe were attempted.
func TestLogGroupParentLister_ParentScopeSkipsDescribe(t *testing.T) {
	t.Parallel()
	args := DiscoverArgs{
		Regions:     []string{"us-east-1"},
		ParentScope: NewParentScope(map[string][]ScopedParent{logGroupParentCFNType: {{Identifier: "/lg/b", Region: "us-east-1"}, {Identifier: "/lg/a", Region: "us-east-1"}}}),
	}
	// awsCfg is a zero Config; if the lister fell through to the SDK path it
	// would construct a real client and (lacking creds/region) the test would
	// not be hermetic. The scope path must short-circuit before that.
	models, err := listCloudWatchLogGroupsAsResourceModels(context.Background(), aws.Config{}, "us-east-1", args)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`{"LogGroupName":"/lg/a"}`, `{"LogGroupName":"/lg/b"}`}
	if !reflect.DeepEqual(models, want) {
		t.Errorf("scoped models = %v, want %v", models, want)
	}
}

// TestLogGroupResourceModels_ScopedMatchesSwept proves the scoped wrap and the
// account-wide wrap emit byte-identical resource models for the same log group
// names — so scoping cannot change downstream LogStream ListResources behavior.
func TestLogGroupResourceModels_ScopedMatchesSwept(t *testing.T) {
	t.Parallel()
	names := []string{"/lg/a", "/lg/b"}
	scoped := logGroupNamesAsResourceModels(names)
	swept, err := listCloudWatchLogGroupsAsResourceModelsWithClient(context.Background(), &fakeCloudWatchLogGroupsLister{
		pages: []cloudwatchlogs.DescribeLogGroupsOutput{cwlLogGroupsPage("", names...)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scoped, swept) {
		t.Errorf("scoped wrap %v != swept wrap %v", scoped, swept)
	}
}

// =====================================================================
// #739 codex follow-up: region-aware ParentScope. These tests FAIL on the
// region-blind code (where scopedParents returned the same identifiers for
// every enumeration region) and pass once scopedParents region-filters.
// =====================================================================

// TestSDKOnlySubresourceDiscover_ParentScopeRegionAware proves a parent scoped
// to us-east-1 is fetched ONLY in us-east-1 across a multi-region request — it
// is NOT re-fetched in eu-west-1. Pre-fix the scoped shortcut returned the same
// identifier for every region, so FetchItem ran in BOTH regions and the closure
// produced duplicate (and eu-west-1-mis-tagged) child imports.
func TestSDKOnlySubresourceDiscover_ParentScopeRegionAware(t *testing.T) {
	t.Parallel()
	// Per-region FetchItem call counter — the region-blind bug doubles this.
	var (
		mu       sync.Mutex
		byRegion = map[string]int{}
	)
	cfg := sdkOnlyTestConfig()
	cfg.ParentCFNType = "AWS::S3::Bucket"
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		t.Error("ListParents must not be called when the type is scoped")
		return nil, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
		mu.Lock()
		byRegion[region]++
		mu.Unlock()
		return true, map[string]any{}, map[string]string{"bucket": parentID}, nil
	}

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			"AWS::S3::Bucket": {{Identifier: "bucket-a", Region: "us-east-1"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if byRegion["us-east-1"] != 1 {
		t.Errorf("FetchItem in us-east-1 = %d, want 1 (the scoped parent's region)", byRegion["us-east-1"])
	}
	if byRegion["eu-west-1"] != 0 {
		t.Errorf("FetchItem in eu-west-1 = %d, want 0 (parent does not live there — region-blind bug re-fetches it)", byRegion["eu-west-1"])
	}
	if len(got) != 1 {
		t.Fatalf("discovered %d child sets, want exactly 1 (region-blind bug duplicates)", len(got))
	}
	if got[0].Identity.Region != "us-east-1" {
		t.Errorf("child region = %q, want us-east-1 (region-blind bug mis-tags the eu-west-1 copy)", got[0].Identity.Region)
	}
}

// TestSDKOnlySubresourceDiscover_ScopedTypeNoParentInRegionSkipsSweep proves the
// (empty, true) contract: a region the scoped type has NO parent in must SKIP
// enumeration entirely, NOT fall back to the account-wide ListParents sweep.
// Pre-fix this region either re-ran the scoped parent (region-blind) or — if
// scopedParents had naively returned (nil, false) for a no-match region — fell
// back to ListParents. Either way ListParents in eu-west-1 must stay at 0.
func TestSDKOnlySubresourceDiscover_ScopedTypeNoParentInRegionSkipsSweep(t *testing.T) {
	t.Parallel()
	var listParentsCalls atomic.Int64
	cfg := sdkOnlyTestConfig()
	cfg.ParentCFNType = "AWS::S3::Bucket"
	cfg.ListParents = func(_ context.Context, _ aws.Config, region string, _ DiscoverArgs) ([]string, error) {
		listParentsCalls.Add(1)
		return []string{"swept-" + region}, nil
	}
	var fetchCalls atomic.Int64
	cfg.FetchItem = func(_ context.Context, _ aws.Config, _, parentID string) (bool, map[string]any, map[string]string, error) {
		fetchCalls.Add(1)
		return true, map[string]any{}, map[string]string{"bucket": parentID}, nil
	}

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			// Only a us-east-1 parent. eu-west-1 is scoped-but-empty.
			"AWS::S3::Bucket": {{Identifier: "bucket-a", Region: "us-east-1"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if listParentsCalls.Load() != 0 {
		t.Errorf("ListParents calls = %d, want 0 (scoped-but-no-parent region must NOT sweep account-wide)", listParentsCalls.Load())
	}
	if fetchCalls.Load() != 1 {
		t.Errorf("FetchItem calls = %d, want 1 (only the us-east-1 parent)", fetchCalls.Load())
	}
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"bucket-a"}) {
		t.Errorf("discovered = %v, want only [bucket-a] (no swept-* from a fallback sweep)", ids)
	}
}

// TestSDKOnlySubresourceDiscover_RegionLessParentEnumeratesOnce proves a
// region-less scoped parent (Region == "", e.g. a global type) is enumerated
// EXACTLY once across a multi-region request — in the first region — not once
// per region. Pre-fix the region-blind shortcut returned it in every region.
func TestSDKOnlySubresourceDiscover_RegionLessParentEnumeratesOnce(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		byRegion = map[string]int{}
	)
	cfg := sdkOnlyTestConfig()
	cfg.ParentCFNType = "AWS::S3::Bucket"
	cfg.ListParents = func(_ context.Context, _ aws.Config, _ string, _ DiscoverArgs) ([]string, error) {
		t.Error("ListParents must not be called when the type is scoped")
		return nil, nil
	}
	cfg.FetchItem = func(_ context.Context, _ aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
		mu.Lock()
		byRegion[region]++
		mu.Unlock()
		return true, map[string]any{}, map[string]string{"bucket": parentID}, nil
	}

	d := newSDKOnlySubresourceDiscoverer(cfg, aws.Config{}, DefaultMaxConcurrency)
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1", "ap-south-1"},
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			// Region-less parent.
			"AWS::S3::Bucket": {{Identifier: "global-parent", Region: ""}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if byRegion["us-east-1"] != 1 {
		t.Errorf("FetchItem in us-east-1 (first region) = %d, want 1", byRegion["us-east-1"])
	}
	if byRegion["eu-west-1"] != 0 || byRegion["ap-south-1"] != 0 {
		t.Errorf("FetchItem in non-first regions = eu-west-1:%d ap-south-1:%d, want 0/0 (region-less parent must enumerate once)", byRegion["eu-west-1"], byRegion["ap-south-1"])
	}
	if len(got) != 1 {
		t.Fatalf("discovered %d child sets, want exactly 1 (region-less parent enumerated once)", len(got))
	}
}

// TestCloudControlDiscover_ParentScopeRegionAware proves the Cloud Control
// scoped-refs seam region-filters: a bucket scoped to us-east-1 is GetResource'd
// only in us-east-1 across a multi-region request — never re-fetched in
// eu-west-1, and the lone result is region-tagged us-east-1.
func TestCloudControlDiscover_ParentScopeRegionAware(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		propsByIdentifier: map[string]map[string]any{
			"bucket-a": {"Tags": map[string]any{}},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::S3::Bucket"
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			"AWS::S3::Bucket": {{Identifier: "bucket-a", Region: "us-east-1"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListResources calls = %d, want 0", fake.listCalls)
	}
	// Region-blind bug: GetResource runs once per region ⇒ 2 calls, 2 IRs.
	if got := len(fake.getResourceCalls); got != 1 {
		t.Errorf("GetResource calls = %d, want 1 (scoped parent fetched only in its region)", got)
	}
	if len(got) != 1 {
		t.Fatalf("discovered %d, want 1 (region-blind bug duplicates across regions)", len(got))
	}
	if got[0].Identity.Region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", got[0].Identity.Region)
	}
}

// TestCloudControlDiscover_ScopedTypeNoParentInRegionSkipsSweep proves the CC
// seam treats (empty, true) as "no parents here, skip" — a region with no scoped
// parent issues ZERO ListResources and ZERO GetResource (no account-wide
// fallback). The us-east-1 region still fetches its one scoped parent.
func TestCloudControlDiscover_ScopedTypeNoParentInRegionSkipsSweep(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		// A fallback sweep would surface these — the scope must never list them.
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "swept-x", "swept-y"),
		},
		propsByIdentifier: map[string]map[string]any{
			"bucket-a": {"Tags": map[string]any{}},
			"swept-x":  {"Tags": map[string]any{}},
			"swept-y":  {"Tags": map[string]any{}},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::S3::Bucket"
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			"AWS::S3::Bucket": {{Identifier: "bucket-a", Region: "us-east-1"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListResources calls = %d, want 0 (scoped-but-empty region must not sweep)", fake.listCalls)
	}
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"bucket-a"}) {
		t.Errorf("discovered = %v, want only [bucket-a] (no swept-* from a fallback sweep)", ids)
	}
}

// TestLogGroupParentLister_RegionAware proves the CloudWatch Logs lister scope
// honors the enumeration region: a log group scoped to us-east-1 yields its
// resource model only when enumerated in us-east-1, and an EMPTY (non-nil)
// model slice in eu-west-1 — so the LogStream discoverer skips eu-west-1 rather
// than re-listing the us-east-1 log group's streams there. Pre-fix the scoped
// shortcut returned the same names for every region.
func TestLogGroupParentLister_RegionAware(t *testing.T) {
	t.Parallel()
	args := DiscoverArgs{
		Regions: []string{"us-east-1", "eu-west-1"},
		ParentScope: NewParentScope(map[string][]ScopedParent{
			logGroupParentCFNType: {{Identifier: "/lg/a", Region: "us-east-1"}},
		}),
	}
	// us-east-1: the scoped log group's model.
	east, err := listCloudWatchLogGroupsAsResourceModels(context.Background(), aws.Config{}, "us-east-1", args)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(east, []string{`{"LogGroupName":"/lg/a"}`}) {
		t.Errorf("us-east-1 models = %v, want the scoped log group", east)
	}
	// eu-west-1: empty (non-nil) — scoped but no parent here, skip without sweep.
	// A zero aws.Config would build a real client and (lacking creds) the test
	// would not be hermetic if the lister fell through to DescribeLogGroups.
	west, err := listCloudWatchLogGroupsAsResourceModels(context.Background(), aws.Config{}, "eu-west-1", args)
	if err != nil {
		t.Fatal(err)
	}
	if west == nil || len(west) != 0 {
		t.Errorf("eu-west-1 models = %v, want empty non-nil slice (scoped-but-no-parent ⇒ skip, no sweep)", west)
	}
}

// TestCloudControlDiscover_ParentScopeWinsOverRGTCache closes codex #770 P2-2:
// a configured ParentScope must take precedence over the RGT prefetch cache.
// The RGT cache only surfaces ARNs that match args.Project, so an explicitly
// selected parent that LACKS the Project tag would be absent from the cache;
// if the cache won, that selected parent would be silently dropped from
// re-discovery and its scoped children would fail to resolve. The scope must
// drive the work set and the cache (a different, tag-matched bucket) must be
// ignored entirely.
func TestCloudControlDiscover_ParentScopeWinsOverRGTCache(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		propsByIdentifier: map[string]map[string]any{
			// The selected bucket carries NO Project tag.
			"bucket-selected": {"Tags": map[string]any{}},
			// The cache-only bucket would be the work set if the cache won.
			"bucket-cached": {"Tags": map[string]any{"Project": "io-foo"}},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::S3::Bucket"
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	// RGT prefetch cache holds only the tag-matched bucket-cached.
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {
			"AWS::S3::Bucket": {
				{ARN: "arn:aws:s3:::bucket-cached", Identifier: "bucket-cached", Tags: map[string]string{"Project": "io-foo"}},
			},
		},
	}}
	args := DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			"AWS::S3::Bucket": {{Identifier: "bucket-selected", Region: "us-east-1"}},
		}),
	}.withRGTCache(cache)

	got, err := d.Discover(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListResources calls = %d, want 0", fake.listCalls)
	}
	// The scope drives the work set, NOT the cache.
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"bucket-selected"}) {
		t.Errorf("discovered = %v, want only the scoped bucket [bucket-selected] (cache must not win, and the untagged scoped bucket must not be tag-dropped)", ids)
	}
}

// TestCloudControlDiscover_GlobalChildScopedRegionLessParent: a region-less
// scoped parent (Region == "") feeding a GLOBAL discoverer (enumerates with
// region == "") is included even when args.Regions is non-empty — the single
// "" pass includes every scoped parent.
func TestCloudControlDiscover_GlobalChildScopedRegionLessParent(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		propsByIdentifier: map[string]map[string]any{
			"role-a": {"Tags": map[string]any{}},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::IAM::Role"
	cfg.IsGlobal = true
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"}, // non-empty, but type is global
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			"AWS::IAM::Role": {{Identifier: "role-a", Region: ""}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"role-a"}) {
		t.Errorf("discovered = %v, want [role-a] (region-less parent must be included in the global pass)", ids)
	}
	if got[0].Identity.Region != "" {
		t.Errorf("region = %q, want \"\" (global resource)", got[0].Identity.Region)
	}
}

// TestCloudControlDiscover_GlobalTypeScopedRealRegionParent closes codex #770
// P1: aws_s3_bucket is IsGlobal-ENUMERATED (single region=="" scan) but its
// IRs carry a REAL Identity.Region (us-west-2, …) promoted by PostDiscover, so
// awsParentScope records the bucket under its true region. The IsGlobal scan
// passes region == "" to scopedParents, which must STILL include the selected
// bucket — region-filtering the single "" pass would drop the bucket entirely
// and break its scoped children. args.Regions is non-empty (the common case).
func TestCloudControlDiscover_GlobalTypeScopedRealRegionParent(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		// A full sweep would surface bucket-other; the scope must skip listing.
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "bucket-west", "bucket-other"),
		},
		propsByIdentifier: map[string]map[string]any{
			"bucket-west":  {"Tags": map[string]any{}},
			"bucket-other": {"Tags": map[string]any{}},
		},
	}
	cfg := testConfig()
	cfg.CloudFormationType = "AWS::S3::Bucket"
	cfg.IsGlobal = true // S3 is enumerated region-less.
	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"}, // non-empty
		AccountID: "123",
		ParentScope: NewParentScope(map[string][]ScopedParent{
			// The selected bucket lives in us-west-2 (its TRUE region),
			// NOT us-east-1 and NOT region-less.
			"AWS::S3::Bucket": {{Identifier: "bucket-west", Region: "us-west-2"}},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListResources calls = %d, want 0 (scope must skip the account-wide list)", fake.listCalls)
	}
	if ids := importIDs(got); !reflect.DeepEqual(ids, []string{"bucket-west"}) {
		t.Errorf("discovered = %v, want [bucket-west] (P1: global single pass must include the scoped real-region bucket, not drop it)", ids)
	}
}

func importIDs(rs []imported.ImportedResource) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Identity.ImportID)
	}
	sort.Strings(out)
	return out
}
