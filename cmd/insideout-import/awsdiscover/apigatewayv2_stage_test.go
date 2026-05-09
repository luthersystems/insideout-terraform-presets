package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
)

var errAPIGWv2StageSeed = errors.New("AccessDenied")

type fakeAPIGWv2StageClient struct {
	// apis is the legacy single-page convenience slice. When apiPages is
	// also non-nil it takes precedence so tests can pin pagination
	// behavior. The single-page apis field is kept for back-compat with
	// tests that don't care about pagination.
	apis        []apigwv2types.Api
	apiPages    []apigatewayv2.GetApisOutput
	stagesByAPI map[string][]apigwv2types.Stage
	stagesByKey map[string]*apigatewayv2.GetStageOutput // "api/stage" -> output

	mu             sync.Mutex
	getApisCalls   []apigatewayv2.GetApisInput
	getStagesCalls []string // ApiId per call
	getStageCalls  []string // "api/stage" per call

	apisErr   error
	stagesErr error
	stageErr  error
}

func (f *fakeAPIGWv2StageClient) GetApis(_ context.Context, in *apigatewayv2.GetApisInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error) {
	f.mu.Lock()
	f.getApisCalls = append(f.getApisCalls, *in)
	idx := len(f.getApisCalls) - 1
	f.mu.Unlock()
	if f.apisErr != nil {
		return nil, f.apisErr
	}
	// When apiPages is configured, walk it like every other paginating
	// fake — return one page per call, terminate when out of pages.
	if len(f.apiPages) > 0 {
		if idx >= len(f.apiPages) {
			return &apigatewayv2.GetApisOutput{}, nil
		}
		out := f.apiPages[idx]
		return &out, nil
	}
	if idx > 0 {
		return &apigatewayv2.GetApisOutput{}, nil
	}
	return &apigatewayv2.GetApisOutput{Items: f.apis}, nil
}

func (f *fakeAPIGWv2StageClient) GetStages(_ context.Context, in *apigatewayv2.GetStagesInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStagesOutput, error) {
	apiID := aws.ToString(in.ApiId)
	f.mu.Lock()
	f.getStagesCalls = append(f.getStagesCalls, apiID)
	f.mu.Unlock()
	if f.stagesErr != nil {
		return nil, f.stagesErr
	}
	stages := f.stagesByAPI[apiID]
	return &apigatewayv2.GetStagesOutput{Items: stages}, nil
}

func (f *fakeAPIGWv2StageClient) GetStage(_ context.Context, in *apigatewayv2.GetStageInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStageOutput, error) {
	key := aws.ToString(in.ApiId) + "/" + aws.ToString(in.StageName)
	f.mu.Lock()
	f.getStageCalls = append(f.getStageCalls, key)
	f.mu.Unlock()
	if f.stageErr != nil {
		return nil, f.stageErr
	}
	if out, ok := f.stagesByKey[key]; ok {
		return out, nil
	}
	return nil, &apigwv2types.NotFoundException{}
}

func stageWithTags(name string, autoDeploy bool, deploymentID string, tags map[string]string) apigwv2types.Stage {
	s := apigwv2types.Stage{
		StageName:  aws.String(name),
		AutoDeploy: aws.Bool(autoDeploy),
	}
	if deploymentID != "" {
		s.DeploymentId = aws.String(deploymentID)
	}
	if tags != nil {
		s.Tags = tags
	}
	return s
}

func TestAPIGWv2StageDiscover_IteratesPerAPI(t *testing.T) {
	t.Parallel()
	apis := []apigwv2types.Api{
		apiWithTags("a1", "io-foo-orders", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil),
		apiWithTags("a2", "io-foo-events", "https://a2.execute-api.us-east-1.amazonaws.com", "HTTP", nil),
		apiWithTags("a3", "other-api", "https://a3.execute-api.us-east-1.amazonaws.com", "HTTP", nil),
	}
	fake := &fakeAPIGWv2StageClient{
		apis: apis,
		stagesByAPI: map[string][]apigwv2types.Stage{
			"a1": {stageWithTags("$default", true, "dep1", map[string]string{"Project": "io-foo"})},
			"a2": {stageWithTags("prod", false, "dep2", map[string]string{"Project": "io-foo"})},
			"a3": {stageWithTags("$default", true, "depX", nil)},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	// Pin: GetStages should have been called only on prefix-matching APIs (a1, a2).
	wantStagesCalls := map[string]bool{"a1": true, "a2": true}
	for _, id := range fake.getStagesCalls {
		if !wantStagesCalls[id] {
			t.Errorf("unexpected GetStages call on api=%q (only prefix-matching APIs should be called)", id)
		}
		delete(wantStagesCalls, id)
	}
	if len(wantStagesCalls) != 0 {
		t.Errorf("missing GetStages calls on %v", wantStagesCalls)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one stage per matching API)", len(got))
	}
	wantImports := map[string]bool{"a1/$default": true, "a2/prod": true}
	for _, ir := range got {
		if !wantImports[ir.Identity.ImportID] {
			t.Errorf("unexpected ImportID=%q", ir.Identity.ImportID)
		}
	}
}

func TestAPIGWv2StageDiscover_ImportIDIsApiSlashStage(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		apis: []apigwv2types.Api{apiWithTags("a1", "io-foo-orders", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
		stagesByAPI: map[string][]apigwv2types.Stage{
			"a1": {stageWithTags("$default", true, "dep1", map[string]string{"Project": "io-foo"})},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.ImportID != "a1/$default" {
		t.Errorf("ImportID=%q, want a1/$default", got[0].Identity.ImportID)
	}
	if got[0].Identity.NativeIDs["api_id"] != "a1" {
		t.Errorf("NativeIDs[api_id]=%q", got[0].Identity.NativeIDs["api_id"])
	}
	if got[0].Identity.NativeIDs["stage_name"] != "$default" {
		t.Errorf("NativeIDs[stage_name]=%q", got[0].Identity.NativeIDs["stage_name"])
	}
	if got[0].Identity.NativeIDs["auto_deploy"] != "true" {
		t.Errorf("NativeIDs[auto_deploy]=%q, want true", got[0].Identity.NativeIDs["auto_deploy"])
	}
	if got[0].Identity.NativeIDs["deployment_id"] != "dep1" {
		t.Errorf("NativeIDs[deployment_id]=%q", got[0].Identity.NativeIDs["deployment_id"])
	}
}

func TestAPIGWv2StageDiscover_TagsAreInline(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		apis: []apigwv2types.Api{apiWithTags("a1", "io-foo-prod", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
		stagesByAPI: map[string][]apigwv2types.Stage{
			"a1": {stageWithTags("prod", false, "dep1", map[string]string{"Project": "io-foo", "env": "prod"})},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q (inline tags propagate)", got[0].Identity.Tags["env"])
	}
}

func TestAPIGWv2StageDiscover_PropagatesAPIsError(t *testing.T) {
	t.Parallel()
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient {
		return &fakeAPIGWv2StageClient{apisErr: errAPIGWv2StageSeed}
	}, maxConcurrency: 4}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errAPIGWv2StageSeed) {
		t.Errorf("err=%v, want errors.Is(err, errAPIGWv2StageSeed)", err)
	}
}

func TestAPIGWv2StageDiscover_PropagatesStagesError(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		apis:      []apigwv2types.Api{apiWithTags("a1", "io-foo-orders", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
		stagesErr: errAPIGWv2StageSeed,
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errAPIGWv2StageSeed) {
		t.Errorf("err=%v, want errors.Is(err, errAPIGWv2StageSeed)", err)
	}
}

func TestAPIGWv2StageDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		apis: []apigwv2types.Api{apiWithTags("a1", "io-foo-orders", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
		stagesByAPI: map[string][]apigwv2types.Stage{
			"a1": {
				stageWithTags("prod", false, "dep1", map[string]string{"Project": "io-foo", "env": "prod"}),
				stageWithTags("staging", false, "dep2", map[string]string{"Project": "io-foo", "env": "staging"}),
			},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:      "io-foo",
		Regions:      []string{"us-east-1"},
		AccountID:    "123",
		TagSelectors: []TagSelector{{Key: "env", Value: "prod"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only env=prod stage)", len(got))
	}
	if got[0].Identity.NativeIDs["stage_name"] != "prod" {
		t.Errorf("stage_name=%q", got[0].Identity.NativeIDs["stage_name"])
	}
}

func TestAPIGWv2StageDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeAPIGWv2StageClient{
		"us-east-1": {
			apis:        []apigwv2types.Api{apiWithTags("a1east", "io-foo-east", "https://a1east.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
			stagesByAPI: map[string][]apigwv2types.Stage{"a1east": {stageWithTags("prod", false, "dep1", map[string]string{"Project": "io-foo"})}},
		},
		"eu-west-1": {
			apis:        []apigwv2types.Api{apiWithTags("a1west", "io-foo-west", "https://a1west.execute-api.eu-west-1.amazonaws.com", "HTTP", nil)},
			stagesByAPI: map[string][]apigwv2types.Stage{"a1west": {stageWithTags("prod", false, "dep2", map[string]string{"Project": "io-foo"})}},
		},
	}
	var seenRegions []string
	d := &apigwV2StageDiscoverer{new: func(region string) apigwV2StageClient {
		seenRegions = append(seenRegions, region)
		return fakes[region]
	}, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v", seenRegions)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
}

func TestAPIGWv2StageDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeAPIGWv2StageClient{
		"us-east-1": {
			apis:        []apigwv2types.Api{apiWithTags("a1", "io-foo-east", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
			stagesByAPI: map[string][]apigwv2types.Stage{"a1": {stageWithTags("prod", false, "dep1", map[string]string{"Project": "io-foo"})}},
		},
		"eu-west-1": {
			apis:        []apigwv2types.Api{apiWithTags("a1", "io-foo-west", "https://a1.execute-api.eu-west-1.amazonaws.com", "HTTP", nil)},
			stagesByAPI: map[string][]apigwv2types.Stage{"a1": {stageWithTags("prod", false, "dep2", map[string]string{"Project": "io-foo"})}},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(region string) apigwV2StageClient { return fakes[region] }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123", Emitter: rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts, finishes := map[string]int{}, map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != "apigatewayv2_stage" {
				t.Errorf("service_start.service=%q", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != "apigatewayv2_stage" {
				t.Errorf("service_finish.service=%q", e.Service)
			}
			finishes[e.Region]++
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if starts[region] != 1 {
			t.Errorf("region=%s: service_start count=%d", region, starts[region])
		}
		if finishes[region] != 1 {
			t.Errorf("region=%s: service_finish count=%d", region, finishes[region])
		}
	}
}

func TestAPIGWv2StageDiscover_EmitsItemFound_PerStage(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		apis: []apigwv2types.Api{apiWithTags("a1", "io-foo-orders", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
		stagesByAPI: map[string][]apigwv2types.Stage{
			"a1": {
				stageWithTags("prod", false, "dep1", map[string]string{"Project": "io-foo"}),
				stageWithTags("staging", false, "dep2", map[string]string{"Project": "io-foo"}),
				stageWithTags("untagged", false, "dep3", map[string]string{"Owner": "team"}),
			},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123", Emitter: rec,
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
		t.Errorf("item_found count=%d, want %d", len(items), len(got))
	}
	wantImports := map[string]bool{"a1/prod": true, "a1/staging": true}
	for _, it := range items {
		if it.Service != "apigatewayv2_stage" {
			t.Errorf("item.service=%q", it.Service)
		}
		if it.TFType != apigwV2StageTFType {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
		if !wantImports[it.ImportID] {
			t.Errorf("item.import_id=%q not in expected set", it.ImportID)
		}
	}
}

func TestAPIGWv2StageDiscoverByID_AcceptsApiSlashStage(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		stagesByKey: map[string]*apigatewayv2.GetStageOutput{
			"a1/prod": {
				StageName:    aws.String("prod"),
				AutoDeploy:   aws.Bool(true),
				DeploymentId: aws.String("dep1"),
			},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), "a1/prod", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != apigwV2StageTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "a1/prod" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["api_id"] != "a1" {
		t.Errorf("NativeIDs[api_id]=%q", got.Identity.NativeIDs["api_id"])
	}
	if got.Identity.NativeIDs["stage_name"] != "prod" {
		t.Errorf("NativeIDs[stage_name]=%q", got.Identity.NativeIDs["stage_name"])
	}
	if got.Identity.NativeIDs["auto_deploy"] != "true" {
		t.Errorf("NativeIDs[auto_deploy]=%q", got.Identity.NativeIDs["auto_deploy"])
	}
}

func TestAPIGWv2StageDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return &fakeAPIGWv2StageClient{} }}
	_, err := d.DiscoverByID(context.Background(), "a1/prod", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestAPIGWv2StageDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return &fakeAPIGWv2StageClient{} }}
	cases := []string{
		"",
		"a1",            // missing slash
		"/prod",         // empty api id
		"a1/",           // empty stage name
		"a1/prod/extra", // bad: deployment id has slash
		"api 1/prod",    // contains space
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestAPIGWv2StageDiscover_PaginatesUntilNoToken pins that the GetApis
// loop walks NextToken-shaped pagination. The fake's GetApis was
// historically hard-coded to a single page; this test exercises the
// (apiPages, NextToken) wiring.
func TestAPIGWv2StageDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		apiPages: []apigatewayv2.GetApisOutput{
			{
				Items:     []apigwv2types.Api{apiWithTags("a1", "io-foo-a", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
				NextToken: aws.String("tok1"),
			},
			{
				Items:     []apigwv2types.Api{apiWithTags("a2", "io-foo-b", "https://a2.execute-api.us-east-1.amazonaws.com", "HTTP", nil)},
				NextToken: aws.String("tok2"),
			},
			{Items: []apigwv2types.Api{apiWithTags("a3", "io-foo-c", "https://a3.execute-api.us-east-1.amazonaws.com", "HTTP", nil)}}, // terminal
		},
		stagesByAPI: map[string][]apigwv2types.Stage{
			"a1": {stageWithTags("prod", false, "dep1", map[string]string{"Project": "io-foo"})},
			"a2": {stageWithTags("prod", false, "dep2", map[string]string{"Project": "io-foo"})},
			"a3": {stageWithTags("prod", false, "dep3", map[string]string{"Project": "io-foo"})},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(fake.getApisCalls) < 3 {
		t.Fatalf("GetApis calls=%d, want >=3", len(fake.getApisCalls))
	}
	if aws.ToString(fake.getApisCalls[1].NextToken) != "tok1" {
		t.Errorf("call[1].NextToken=%q, want tok1", aws.ToString(fake.getApisCalls[1].NextToken))
	}
	if aws.ToString(fake.getApisCalls[2].NextToken) != "tok2" {
		t.Errorf("call[2].NextToken=%q, want tok2", aws.ToString(fake.getApisCalls[2].NextToken))
	}
}

// TestAPIGWv2StageDiscover_EmptyProjectReturnsAll pins that an empty
// Project disables the API name-prefix gate.
func TestAPIGWv2StageDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2StageClient{
		apis: []apigwv2types.Api{
			apiWithTags("a1", "io-foo-orders", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", nil),
			apiWithTags("a2", "other-api", "https://a2.execute-api.us-east-1.amazonaws.com", "HTTP", nil),
		},
		stagesByAPI: map[string][]apigwv2types.Stage{
			"a1": {stageWithTags("prod", false, "dep1", nil)},
			"a2": {stageWithTags("prod", false, "dep2", nil)},
		},
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (no prefix filter)", len(got))
	}
}

// blockingAPIGWv2StageClient drives the per-API GetStages fan-out under
// an errgroup. Used for the bounded-concurrency test below.
type blockingAPIGWv2StageClient struct {
	apis        []apigwv2types.Api
	stagesByAPI map[string][]apigwv2types.Stage

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int

	apisIdx int
}

func (c *blockingAPIGWv2StageClient) GetApis(_ context.Context, _ *apigatewayv2.GetApisInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error) {
	if c.apisIdx == 0 {
		c.apisIdx++
		return &apigatewayv2.GetApisOutput{Items: c.apis}, nil
	}
	return &apigatewayv2.GetApisOutput{}, nil
}

func (c *blockingAPIGWv2StageClient) GetStages(ctx context.Context, in *apigatewayv2.GetStagesInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStagesOutput, error) {
	c.mu.Lock()
	c.inflight++
	if c.inflight > c.maxInflight {
		c.maxInflight = c.inflight
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inflight--
		c.mu.Unlock()
	}()
	select {
	case <-c.release:
		return &apigatewayv2.GetStagesOutput{Items: c.stagesByAPI[aws.ToString(in.ApiId)]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *blockingAPIGWv2StageClient) GetStage(_ context.Context, _ *apigatewayv2.GetStageInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStageOutput, error) {
	return nil, errors.New("blockingAPIGWv2StageClient.GetStage: unused")
}

// TestAPIGWv2StageDiscover_BoundedConcurrency pins the per-API GetStages
// fan-out under the configured concurrency limit.
func TestAPIGWv2StageDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4
	apis := make([]apigwv2types.Api, total)
	stagesByAPI := make(map[string][]apigwv2types.Stage, total)
	for i := 0; i < total; i++ {
		apiID := fmt.Sprintf("a-%d", i)
		apis[i] = apiWithTags(apiID, fmt.Sprintf("io-foo-%d", i), "https://x.example", "HTTP", nil)
		stagesByAPI[apiID] = []apigwv2types.Stage{stageWithTags("prod", false, fmt.Sprintf("dep-%d", i), map[string]string{"Project": "io-foo"})}
	}
	release := make(chan struct{})
	bc := &blockingAPIGWv2StageClient{
		apis:        apis,
		stagesByAPI: stagesByAPI,
		release:     release,
	}
	d := &apigwV2StageDiscoverer{new: func(_ string) apigwV2StageClient { return bc }, maxConcurrency: limit}
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
		done <- err
	}()
	deadline := time.After(2 * time.Second)
	for {
		bc.mu.Lock()
		got := bc.inflight
		bc.mu.Unlock()
		if got >= limit {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("never reached %d in-flight; saw %d", limit, got)
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)
	bc.mu.Lock()
	peak := bc.maxInflight
	bc.mu.Unlock()
	if peak > limit {
		t.Errorf("peak in-flight=%d exceeded limit=%d", peak, limit)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
}
