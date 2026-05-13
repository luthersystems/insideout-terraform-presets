package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	smithy "github.com/aws/smithy-go"
)

// errCCSeed is the package-level sentinel returned by the fake Cloud
// Control client in tests that assert error propagation. Tests should
// use errors.Is(err, errCCSeed) rather than checking only `err != nil`
// — the latter masks regressions where the discoverer silently swallows
// the SDK error and returns a different one.
var errCCSeed = errors.New("AccessDenied")

// fakeCloudControlClient implements cloudControlClient for unit tests.
// Construction time fields seed the ListResources / GetResource
// responses; runtime fields record observed inputs for assertions.
//
// Per-region observation: production code constructs one client per
// region via the cloudControlDiscoverer.new closure; tests inject a
// per-region fake via a map keyed by region so the test asserts the
// closure was invoked once per region.
type fakeCloudControlClient struct {
	mu sync.Mutex

	// ListResources wiring. Each call returns one page; pagination is
	// emulated by emitting a NextToken on every page except the last
	// one in listPages. A non-empty listResourceModel passed by the
	// production discoverer (parent-scoped types) is recorded under
	// listResourceModelsSeen.
	listPages              []cloudcontrol.ListResourcesOutput
	listCalls              int
	listResourceModelsSeen []string
	listErr                error

	// GetResource wiring. propsByIdentifier maps the per-resource
	// identifier to a (JSON-serialized) properties map; tests usually
	// populate via the propsFor() helper to avoid raw JSON in tests.
	// getResourceErrByIdentifier returns a per-identifier error
	// (overrides the success path for that identifier only).
	// getResourceErr is a blanket error for every GetResource call.
	propsByIdentifier          map[string]map[string]any
	getResourceErrByIdentifier map[string]error
	getResourceErr             error
	getResourceCalls           []string
}

func (f *fakeCloudControlClient) ListResources(_ context.Context, in *cloudcontrol.ListResourcesInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.ResourceModel != nil {
		f.listResourceModelsSeen = append(f.listResourceModelsSeen, *in.ResourceModel)
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	idx := f.listCalls
	f.listCalls++
	if idx >= len(f.listPages) {
		return &cloudcontrol.ListResourcesOutput{}, nil
	}
	return &f.listPages[idx], nil
}

func (f *fakeCloudControlClient) GetResource(_ context.Context, in *cloudcontrol.GetResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error) {
	f.mu.Lock()
	f.getResourceCalls = append(f.getResourceCalls, *in.Identifier)
	perIDErr, hasPerID := f.getResourceErrByIdentifier[*in.Identifier]
	f.mu.Unlock()
	if hasPerID {
		return nil, perIDErr
	}
	if f.getResourceErr != nil {
		return nil, f.getResourceErr
	}
	props, ok := f.propsByIdentifier[*in.Identifier]
	if !ok {
		return nil, &cctypes.ResourceNotFoundException{Message: ptr("not found")}
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return nil, err
	}
	rawStr := string(raw)
	return &cloudcontrol.GetResourceOutput{
		TypeName: in.TypeName,
		ResourceDescription: &cctypes.ResourceDescription{
			Identifier: in.Identifier,
			Properties: &rawStr,
		},
	}, nil
}

// listPage builds a ListResourcesOutput for a set of identifiers, with
// an optional NextToken for pagination.
func listPage(token string, identifiers ...string) cloudcontrol.ListResourcesOutput {
	descs := make([]cctypes.ResourceDescription, 0, len(identifiers))
	for _, id := range identifiers {
		descs = append(descs, cctypes.ResourceDescription{Identifier: &id})
	}
	out := cloudcontrol.ListResourcesOutput{ResourceDescriptions: descs}
	if token != "" {
		out.NextToken = &token
	}
	return out
}

// testConfig builds a baseline cloudControlConfig pointed at the test
// CloudFormation type with sensible defaults — name and import-ID
// extractors pass identifier through, tag extractor reads the flat
// "tags" key. Tests override fields per scenario.
func testConfig() cloudControlConfig {
	return cloudControlConfig{
		TFType:             "aws_test_resource",
		CloudFormationType: "AWS::Test::Resource",
		Slug:               "testres",
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "Tags")
		},
	}
}

// TestCloudControlDiscover_HappyPath exercises the full read path:
// ListResources → per-id GetResource fan-out → tag extraction →
// MatchesAll filter (none here) → ImportedResource emission. Pins
// that Identity.Type, Identity.ImportID, Identity.NativeIDs["name"],
// and Identity.Tags propagate as expected.
func TestCloudControlDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "vault-a", "vault-b"),
		},
		propsByIdentifier: map[string]map[string]any{
			"vault-a": {"BackupVaultName": "vault-a", "Tags": map[string]any{"Project": "io-foo"}},
			"vault-b": {"BackupVaultName": "vault-b", "Tags": map[string]any{"Project": "io-foo"}},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != "aws_test_resource" {
			t.Errorf("Type=%q", ir.Identity.Type)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if ir.Identity.NativeIDs["name"] == "" {
			t.Error("NativeIDs[name] empty")
		}
		if ir.Identity.Tags["Project"] != "io-foo" {
			t.Errorf("Tags[Project]=%q, want io-foo", ir.Identity.Tags["Project"])
		}
	}
	// Output sorted by identifier — deterministic order.
	if got[0].Identity.NameHint != "vault-a" {
		t.Errorf("first NameHint=%q, want vault-a (sorted)", got[0].Identity.NameHint)
	}
}

// TestCloudControlDiscover_PaginatesUntilNoToken pins that pagination
// continues until a page returns no NextToken. A regression that drops
// the paginator loop (or only reads the first page) would only see
// one of the three identifiers.
func TestCloudControlDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("tok1", "a"),
			listPage("tok2", "b"),
			listPage("", "c"),
		},
		propsByIdentifier: map[string]map[string]any{
			"a": {}, "b": {}, "c": {},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if fake.listCalls != 3 {
		t.Errorf("listCalls=%d, want 3", fake.listCalls)
	}
}

// TestCloudControlDiscover_PropagatesListError pins that an SDK error
// on ListResources surfaces verbatim (via errors.Is) rather than being
// swallowed or rewrapped into a different error. A regression that
// silently returns nil-error on ListResources failure survives only
// here.
func TestCloudControlDiscover_PropagatesListError(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{listErr: errCCSeed}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	_, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errCCSeed) {
		t.Errorf("err=%v, want errors.Is(err, errCCSeed)", err)
	}
}

// TestCloudControlDiscover_PerItemGetResourceSoftFails pins the
// service_warn soft-fail posture for per-item GetResource errors. The
// expected behavior: warn-and-skip-and-continue, NOT abort the region.
// Matches the gcpdiscover Bundle 11 non-CAI fanout posture so one
// throttled resource does not invalidate a whole region's scope.
func TestCloudControlDiscover_PerItemGetResourceSoftFails(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "good", "bad", "alsogood"),
		},
		propsByIdentifier: map[string]map[string]any{
			"good":     {},
			"alsogood": {},
			// "bad" not present in propsByIdentifier and not in
			// getResourceErrByIdentifier — but we route it to a
			// permission error explicitly:
		},
		getResourceErrByIdentifier: map[string]error{
			"bad": errors.New("AccessDeniedException: not authorized"),
		},
	}
	rec := &recordingEmitter{}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatalf("expected soft-fail, got error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one was soft-failed)", len(got))
	}
	var warns []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "service_warn" {
			warns = append(warns, e)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("warns=%d, want 1 (bad item)", len(warns))
	}
	if warns[0].Service != "testres" {
		t.Errorf("warn service=%q, want testres", warns[0].Service)
	}
	if warns[0].Region != "us-east-1" {
		t.Errorf("warn region=%q, want us-east-1", warns[0].Region)
	}
	if !strings.Contains(warns[0].Message, "bad") {
		t.Errorf("warn message=%q does not mention bad identifier", warns[0].Message)
	}
}

// TestCloudControlDiscover_MultiRegionTriggersOneCallPerRegion pins
// the per-region SDK fanout — the closure is invoked once per region
// in args.Regions and each per-region fake observes a ListResources
// call. Matches the canonical sqs_test.go regional pin.
func TestCloudControlDiscover_MultiRegionTriggersOneCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeCloudControlClient{
		"us-east-1": {
			listPages: []cloudcontrol.ListResourcesOutput{
				listPage("", "east-a"),
			},
			propsByIdentifier: map[string]map[string]any{"east-a": {}},
		},
		"eu-west-1": {
			listPages: []cloudcontrol.ListResourcesOutput{
				listPage("", "west-b"),
			},
			propsByIdentifier: map[string]map[string]any{"west-b": {}},
		},
	}
	var seen []string
	d := &cloudControlDiscoverer{
		cfg: testConfig(),
		new: func(region string) cloudControlClient {
			seen = append(seen, region)
			f, ok := fakes[region]
			if !ok {
				t.Fatalf("closure called with unexpected region %q", region)
			}
			return f
		},
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "us-east-1" || seen[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v, want [us-east-1 eu-west-1]", seen)
	}
	if fakes["us-east-1"].listCalls == 0 {
		t.Error("us-east-1 fake never received ListResources")
	}
	if fakes["eu-west-1"].listCalls == 0 {
		t.Error("eu-west-1 fake never received ListResources")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
}

// TestCloudControlDiscover_GlobalDoesntFanOutByRegion pins that
// IsGlobal=true short-circuits the args.Regions loop and issues a
// single call with region="". A regression that keeps the per-region
// loop for global types would over-call (and produce duplicates).
func TestCloudControlDiscover_GlobalDoesntFanOutByRegion(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "global-a"),
		},
		propsByIdentifier: map[string]map[string]any{"global-a": {}},
	}
	var seenRegions []string
	cfg := testConfig()
	cfg.IsGlobal = true
	d := &cloudControlDiscoverer{
		cfg: cfg,
		new: func(region string) cloudControlClient {
			seenRegions = append(seenRegions, region)
			return fake
		},
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"}, // ignored for global
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 1 || seenRegions[0] != "" {
		t.Errorf("seenRegions=%v, want [\"\"] (global short-circuits per-region loop)", seenRegions)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.Region != "" {
		t.Errorf("Region=%q, want empty for global type", got[0].Identity.Region)
	}
}

// TestCloudControlDiscover_TagSelectorAppliedAsFilter pins the
// in-loop MatchesAll(tags, selectors) filter. A regression that drops
// the filter (or inverts the condition) survives only here. The
// selector matches env=prod; the staging row is rejected.
func TestCloudControlDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "prod", "staging"),
		},
		propsByIdentifier: map[string]map[string]any{
			"prod":    {"Tags": map[string]any{"env": "prod"}},
			"staging": {"Tags": map[string]any{"env": "staging"}},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:      []string{"us-east-1"},
		AccountID:    "123",
		TagSelectors: []TagSelector{{Key: "env", Value: "prod"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only env=prod should pass)", len(got))
	}
	if got[0].Identity.NameHint != "prod" {
		t.Errorf("NameHint=%q, want prod", got[0].Identity.NameHint)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod", got[0].Identity.Tags["env"])
	}
}

// TestCloudControlDiscover_ProjectTagLegacyFilter pins the back-compat
// args.Project="" → tags["Project"] equality filter (matches
// lambda.go:161 posture). Tests both inclusion (matching Project) and
// exclusion (mismatching Project) in one fixture.
func TestCloudControlDiscover_ProjectTagLegacyFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "io-foo-a", "io-bar-b", "no-tag-c"),
		},
		propsByIdentifier: map[string]map[string]any{
			"io-foo-a": {"Tags": map[string]any{"Project": "io-foo"}},
			"io-bar-b": {"Tags": map[string]any{"Project": "io-bar"}},
			"no-tag-c": {"Tags": map[string]any{}}, // empty tags
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only io-foo-a should pass legacy Project filter)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-a" {
		t.Errorf("NameHint=%q, want io-foo-a", got[0].Identity.NameHint)
	}
}

// TestCloudControlDiscover_ParentListerFansOutPerParent pins the
// parent-scoped enumeration: ParentLister returns multiple
// resource-model strings, and ListResources is invoked once per
// parent with the per-parent ResourceModel threaded through.
func TestCloudControlDiscover_ParentListerFansOutPerParent(t *testing.T) {
	t.Parallel()
	// Per-parent identifiers: pool A has clients aa, ab; pool B has bb.
	fake := &fakeCloudControlClient{
		propsByIdentifier: map[string]map[string]any{
			"aa": {"UserPoolId": "A", "ClientId": "aa"},
			"ab": {"UserPoolId": "A", "ClientId": "ab"},
			"bb": {"UserPoolId": "B", "ClientId": "bb"},
		},
	}
	// We can't seed listPages naively because the fake returns the
	// same pages for every ListResources call regardless of input.
	// Instead, route through a per-parent-list page builder.
	parentACalls := 0
	parentBCalls := 0
	originalListResources := fake.ListResources
	_ = originalListResources
	listMux := func(in *cloudcontrol.ListResourcesInput) (*cloudcontrol.ListResourcesOutput, error) {
		if in.ResourceModel == nil {
			return nil, errors.New("expected ResourceModel for parent-scoped list")
		}
		switch *in.ResourceModel {
		case `{"UserPoolId":"A"}`:
			parentACalls++
			page := listPage("", "aa", "ab")
			return &page, nil
		case `{"UserPoolId":"B"}`:
			parentBCalls++
			page := listPage("", "bb")
			return &page, nil
		default:
			return nil, errors.New("unexpected parent model: " + *in.ResourceModel)
		}
	}
	cfg := testConfig()
	cfg.ParentLister = func(_ context.Context, _ cloudControlClient, _ DiscoverArgs) ([]string, error) {
		return []string{`{"UserPoolId":"A"}`, `{"UserPoolId":"B"}`}, nil
	}
	d := &cloudControlDiscoverer{
		cfg: cfg,
		new: func(_ string) cloudControlClient {
			// Re-wire fake to delegate list calls through listMux so
			// per-parent enumeration is observable.
			return &parentMuxClient{listFn: listMux, getFn: fake.GetResource}
		},
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parentACalls != 1 || parentBCalls != 1 {
		t.Errorf("parent calls A=%d B=%d, want 1 each", parentACalls, parentBCalls)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (2 from A, 1 from B)", len(got))
	}
}

// parentMuxClient is a test-only delegator that lets a test route
// per-call ListResources / GetResource through user-provided closures
// while still satisfying cloudControlClient. Used by the parent-lister
// fan-out test to inject ResourceModel-aware behavior without making
// the base fake more complex.
type parentMuxClient struct {
	listFn func(in *cloudcontrol.ListResourcesInput) (*cloudcontrol.ListResourcesOutput, error)
	getFn  func(ctx context.Context, in *cloudcontrol.GetResourceInput, opts ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error)
}

func (c *parentMuxClient) ListResources(_ context.Context, in *cloudcontrol.ListResourcesInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error) {
	return c.listFn(in)
}

func (c *parentMuxClient) GetResource(ctx context.Context, in *cloudcontrol.GetResourceInput, opts ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error) {
	return c.getFn(ctx, in, opts...)
}

// TestCloudControlDiscoverByID_HappyPath pins single-resource lookup:
// one GetResource call, ImportedResource emitted with correct
// Type/NameHint/ImportID/Tags.
func TestCloudControlDiscoverByID_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		propsByIdentifier: map[string]map[string]any{
			"vault-x": {"BackupVaultName": "vault-x", "Tags": map[string]any{"env": "dev"}},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.DiscoverByID(context.Background(), "vault-x", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_test_resource" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "vault-x" {
		t.Errorf("ImportID=%q, want vault-x", got.Identity.ImportID)
	}
	if got.Identity.Tags["env"] != "dev" {
		t.Errorf("Tags[env]=%q, want dev", got.Identity.Tags["env"])
	}
}

// TestCloudControlDiscoverByID_NotFound pins that ResourceNotFoundException
// (typed or via smithy APIError ErrorCode) maps to ErrNotFound so the
// Stage-2c3 dep-chase loop can convert it to an operator-facing warning.
func TestCloudControlDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{} // empty propsByIdentifier → not-found
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// TestCloudControlDiscoverByID_NotFound_SmithyShape pins the fallback
// path where the SDK returns a generic smithy.APIError with the
// ResourceNotFoundException code rather than the typed exception.
// Defensive: the typed-exception form may evolve in future SDKs.
func TestCloudControlDiscoverByID_NotFound_SmithyShape(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		getResourceErr: &smithy.GenericAPIError{
			Code:    "ResourceNotFoundException",
			Message: "not found",
			Fault:   smithy.FaultClient,
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	_, err := d.DiscoverByID(context.Background(), "anything", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound (via smithy ErrorCode)", err)
	}
}

// TestCloudControlDiscoverByID_UnsupportedID pins the empty-id check.
// An empty id is rejected as ErrNotSupported (matches the
// {lambda,sqs}NameFromID pattern).
func TestCloudControlDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return &fakeCloudControlClient{} },
		maxConcurrency: DefaultMaxConcurrency,
	}
	_, err := d.DiscoverByID(context.Background(), "  ", "us-east-1", "123")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("err=%v, want ErrNotSupported", err)
	}
}

// TestCloudControlDiscover_EmitsServiceStartFinish_PerRegion pins the
// per-service progress contract (#295): each region gets one
// service_start and one service_finish event, in that order, with
// the correct slug.
func TestCloudControlDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeCloudControlClient{
		"us-east-1": {
			listPages: []cloudcontrol.ListResourcesOutput{listPage("", "east-a")},
			propsByIdentifier: map[string]map[string]any{
				"east-a": {},
			},
		},
		"eu-west-1": {
			listPages: []cloudcontrol.ListResourcesOutput{listPage("", "west-b")},
			propsByIdentifier: map[string]map[string]any{
				"west-b": {},
			},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(region string) cloudControlClient { return fakes[region] },
		maxConcurrency: DefaultMaxConcurrency,
	}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	events := rec.snapshot()

	type bracket struct{ start, finish int }
	got := map[string]bracket{}
	for i, e := range events {
		switch e.Kind {
		case "service_start":
			b := got[e.Region]
			b.start = i + 1
			got[e.Region] = b
		case "service_finish":
			b := got[e.Region]
			b.finish = i + 1
			got[e.Region] = b
		}
		if e.Kind == "service_start" || e.Kind == "service_finish" {
			if e.Service != "testres" {
				t.Errorf("event %d: service=%q, want testres", i, e.Service)
			}
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		b := got[region]
		if b.start == 0 || b.finish == 0 {
			t.Errorf("region %s: missing service_start or service_finish: %+v", region, b)
		}
		if b.start >= b.finish {
			t.Errorf("region %s: start at index %d >= finish at index %d", region, b.start, b.finish)
		}
	}
}

// TestCloudControlDiscover_EmitsItemFound_PerResource pins that one
// item_found event fires per emitted ImportedResource, carrying the
// TF type and import ID. A regression that emits one event per page
// (rather than per resource) would surface as a count mismatch.
func TestCloudControlDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "a", "b", "c"),
		},
		propsByIdentifier: map[string]map[string]any{
			"a": {}, "b": {}, "c": {},
		},
	}
	d := &cloudControlDiscoverer{
		cfg:            testConfig(),
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "item_found" {
			items = append(items, e)
		}
	}
	if len(items) != len(got) {
		t.Errorf("item_found count=%d, want %d (one per emitted resource)", len(items), len(got))
	}
	for _, it := range items {
		if it.TFType != "aws_test_resource" {
			t.Errorf("item TFType=%q, want aws_test_resource", it.TFType)
		}
	}
}

// TestExtractStringMap pins the JSON-flat-map tag extractor — used by
// types whose tags are encoded as {"Tags": {"key":"value"}} in the
// Cloud Control properties payload.
func TestExtractStringMap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		props map[string]any
		key   string
		want  map[string]string
	}{
		{
			name:  "nil props returns nil",
			props: nil,
			key:   "Tags",
			want:  nil,
		},
		{
			name:  "missing key returns nil",
			props: map[string]any{"OtherKey": "v"},
			key:   "Tags",
			want:  nil,
		},
		{
			name:  "empty map returns empty (non-nil)",
			props: map[string]any{"Tags": map[string]any{}},
			key:   "Tags",
			want:  map[string]string{},
		},
		{
			name:  "populated map round-trips",
			props: map[string]any{"Tags": map[string]any{"env": "prod", "team": "platform"}},
			key:   "Tags",
			want:  map[string]string{"env": "prod", "team": "platform"},
		},
		{
			name:  "non-map value returns nil",
			props: map[string]any{"Tags": "not-a-map"},
			key:   "Tags",
			want:  nil,
		},
		{
			name:  "non-string values are skipped",
			props: map[string]any{"Tags": map[string]any{"keep": "yes", "drop": 42}},
			key:   "Tags",
			want:  map[string]string{"keep": "yes"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractStringMap(tc.props, tc.key)
			if !mapEqual(got, tc.want) {
				t.Errorf("got=%v, want=%v", got, tc.want)
			}
		})
	}
}

// TestExtractTagList pins the AWS-list-of-Key-Value tag extractor —
// used by types whose tags are encoded as [{"Key":"k","Value":"v"}].
func TestExtractTagList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		props map[string]any
		key   string
		want  map[string]string
	}{
		{
			name:  "nil returns nil",
			props: nil,
			key:   "Tags",
			want:  nil,
		},
		{
			name:  "missing key returns nil",
			props: map[string]any{"Other": "v"},
			key:   "Tags",
			want:  nil,
		},
		{
			name:  "empty slice returns empty (non-nil)",
			props: map[string]any{"Tags": []any{}},
			key:   "Tags",
			want:  map[string]string{},
		},
		{
			name: "populated list round-trips",
			props: map[string]any{
				"Tags": []any{
					map[string]any{"Key": "env", "Value": "prod"},
					map[string]any{"Key": "team", "Value": "platform"},
				},
			},
			key:  "Tags",
			want: map[string]string{"env": "prod", "team": "platform"},
		},
		{
			name: "entries without Key are skipped",
			props: map[string]any{
				"Tags": []any{
					map[string]any{"Value": "v-only"},
					map[string]any{"Key": "k1", "Value": "v1"},
				},
			},
			key:  "Tags",
			want: map[string]string{"k1": "v1"},
		},
		{
			name:  "non-slice returns nil",
			props: map[string]any{"Tags": "not-a-slice"},
			key:   "Tags",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractTagList(tc.props, tc.key)
			if !mapEqual(got, tc.want) {
				t.Errorf("got=%v, want=%v", got, tc.want)
			}
		})
	}
}

// TestExtractString pins the string-field extractor used by per-type
// NameHintFromProperties when the property lives at a known key.
func TestExtractString(t *testing.T) {
	t.Parallel()
	if got := extractString(nil, "k"); got != "" {
		t.Errorf("nil props: got %q, want \"\"", got)
	}
	if got := extractString(map[string]any{"k": "v"}, "k"); got != "v" {
		t.Errorf("k→v: got %q, want v", got)
	}
	if got := extractString(map[string]any{"k": 42}, "k"); got != "" {
		t.Errorf("non-string value: got %q, want \"\"", got)
	}
	if got := extractString(map[string]any{}, "missing"); got != "" {
		t.Errorf("missing key: got %q, want \"\"", got)
	}
}

// mapEqual is a small helper to avoid pulling in reflect.DeepEqual /
// testify when comparing string maps. nil and an empty map are NOT
// equal under this comparison — that distinction is load-bearing for
// the nil-vs-empty contract on tag extractors.
func mapEqual(a, b map[string]string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
