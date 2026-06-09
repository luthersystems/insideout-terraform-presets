package awsdiscover

import (
	"context"
	"reflect"
	"sort"
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
	scope := NewParentScope(map[string][]string{
		"AWS::S3::Bucket":     {"b-2", "b-1", "b-2", "  ", "b-1"},
		"AWS::Logs::LogGroup": {" lg ", "lg"},
		"  ":                  {"ignored"}, // empty CFN type dropped
		"AWS::Empty::Type":    {"", "   "}, // no usable ids dropped
	})
	if got := scope["AWS::S3::Bucket"]; !reflect.DeepEqual(got, []string{"b-1", "b-2"}) {
		t.Errorf("S3 scope = %v, want sorted de-duped [b-1 b-2]", got)
	}
	if got := scope["AWS::Logs::LogGroup"]; !reflect.DeepEqual(got, []string{"lg"}) {
		t.Errorf("Logs scope = %v, want trimmed de-duped [lg]", got)
	}
	if _, ok := scope["  "]; ok {
		t.Error("empty CFN type should be dropped")
	}
	if _, ok := scope["AWS::Empty::Type"]; ok {
		t.Error("type with no usable ids should be dropped")
	}
	if NewParentScope(map[string][]string{"x": {""}}) != nil {
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
		ParentScope: NewParentScope(map[string][]string{"AWS::S3::Bucket": {"bucket-a", "bucket-b"}}),
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
		ParentScope: NewParentScope(map[string][]string{"AWS::S3::Bucket": {"bucket-a"}}),
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
		ParentScope: NewParentScope(map[string][]string{"AWS::S3::Bucket": {"bucket-a", "bucket-b"}}),
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
		ParentScope: NewParentScope(map[string][]string{logGroupParentCFNType: {"/lg/b", "/lg/a"}}),
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

func importIDs(rs []imported.ImportedResource) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Identity.ImportID)
	}
	sort.Strings(out)
	return out
}
