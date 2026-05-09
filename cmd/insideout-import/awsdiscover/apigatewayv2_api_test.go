package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
)

var errAPIGWv2APISeed = errors.New("AccessDenied")

type fakeAPIGWv2APIClient struct {
	pages     []apigatewayv2.GetApisOutput
	getByID   map[string]*apigatewayv2.GetApiOutput
	listErr   error
	getErr    error
	listCalls []apigatewayv2.GetApisInput
	getCalls  []string
}

func (f *fakeAPIGWv2APIClient) GetApis(_ context.Context, in *apigatewayv2.GetApisInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error) {
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	if f.listErr != nil {
		return nil, f.listErr
	}
	if idx >= len(f.pages) {
		return &apigatewayv2.GetApisOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeAPIGWv2APIClient) GetApi(_ context.Context, in *apigatewayv2.GetApiInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApiOutput, error) {
	id := aws.ToString(in.ApiId)
	f.getCalls = append(f.getCalls, id)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if out, ok := f.getByID[id]; ok {
		return out, nil
	}
	return nil, &apigwv2types.NotFoundException{}
}

func apiWithTags(id, name, endpoint, protocol string, tags map[string]string) apigwv2types.Api {
	api := apigwv2types.Api{
		ApiId:                    aws.String(id),
		Name:                     aws.String(name),
		ApiEndpoint:              aws.String(endpoint),
		ProtocolType:             apigwv2types.ProtocolType(protocol),
		RouteSelectionExpression: aws.String("$request.method $request.path"),
	}
	if tags != nil {
		api.Tags = tags
	}
	return api
}

func TestAPIGWv2APIDiscover_PrefixThenTagFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIClient{
		pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1", "io-foo-orders", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
			apiWithTags("a2", "io-foo-events", "https://a2.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
			apiWithTags("a3", "other-api", "https://a3.execute-api.us-east-1.amazonaws.com", "HTTP", nil),
			apiWithTags("a4", "io-foo-untagged", "https://a4.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Owner": "team"}),
		}}},
	}
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (prefix + tag filter)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NativeIDs["api_id"] == "" {
			t.Error("NativeIDs[api_id] empty")
		}
		if ir.Identity.NativeIDs["protocol_type"] != "HTTP" {
			t.Errorf("NativeIDs[protocol_type]=%q, want HTTP", ir.Identity.NativeIDs["protocol_type"])
		}
		if ir.Identity.NativeIDs["endpoint"] == "" {
			t.Error("NativeIDs[endpoint] empty")
		}
	}
}

func TestAPIGWv2APIDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIClient{
		pages: []apigatewayv2.GetApisOutput{
			{
				Items: []apigwv2types.Api{
					apiWithTags("a1", "io-foo-a", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
				},
				NextToken: aws.String("tok1"),
			},
			{Items: []apigwv2types.Api{
				apiWithTags("a2", "io-foo-b", "https://a2.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
			}}, // terminal
		},
	}
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (paginated)", len(got))
	}
}

func TestAPIGWv2APIDiscover_TagsAreInline(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIClient{
		pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1", "io-foo-prod", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP",
				map[string]string{"Project": "io-foo", "env": "prod"}),
		}}},
	}
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (inline tags propagate)", got[0].Identity.Tags["env"])
	}
}

func TestAPIGWv2APIDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient {
		return &fakeAPIGWv2APIClient{listErr: errAPIGWv2APISeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errAPIGWv2APISeed) {
		t.Errorf("err=%v, want errors.Is(err, errAPIGWv2APISeed)", err)
	}
}

func TestAPIGWv2APIDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIClient{
		pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1", "io-foo-prod", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP",
				map[string]string{"Project": "io-foo", "env": "prod"}),
			apiWithTags("a2", "io-foo-staging", "https://a2.execute-api.us-east-1.amazonaws.com", "HTTP",
				map[string]string{"Project": "io-foo", "env": "staging"}),
		}}},
	}
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return fake }}
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
		t.Fatalf("len=%d, want 1 (only env=prod)", len(got))
	}
	if got[0].Identity.NameHint != "io-foo-prod" {
		t.Errorf("NameHint=%q", got[0].Identity.NameHint)
	}
}

func TestAPIGWv2APIDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeAPIGWv2APIClient{
		"us-east-1": {pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1east", "io-foo-east", "https://a1east.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
		}}}},
		"eu-west-1": {pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1west", "io-foo-west", "https://a1west.execute-api.eu-west-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
		}}}},
	}
	var seenRegions []string
	d := &apigwV2APIDiscoverer{new: func(region string) apigwV2APIClient {
		seenRegions = append(seenRegions, region)
		f, ok := fakes[region]
		if !ok {
			t.Fatalf("closure called with unexpected region %q", region)
		}
		return f
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seenRegions) != 2 || seenRegions[0] != "us-east-1" || seenRegions[1] != "eu-west-1" {
		t.Errorf("region closure invocations = %v, want [us-east-1 eu-west-1]", seenRegions)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
}

func TestAPIGWv2APIDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeAPIGWv2APIClient{
		"us-east-1": {pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1east", "io-foo-east", "https://a1east.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
		}}}},
		"eu-west-1": {pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1west", "io-foo-west", "https://a1west.execute-api.eu-west-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
		}}}},
	}
	d := &apigwV2APIDiscoverer{new: func(region string) apigwV2APIClient { return fakes[region] }}
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
			if e.Service != "apigatewayv2_api" {
				t.Errorf("service_start.service=%q", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != "apigatewayv2_api" {
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

func TestAPIGWv2APIDiscover_EmitsItemFound_PerAPI(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIClient{
		pages: []apigatewayv2.GetApisOutput{{Items: []apigwv2types.Api{
			apiWithTags("a1", "io-foo-a", "https://a1.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
			apiWithTags("a2", "io-foo-b", "https://a2.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Project": "io-foo"}),
			apiWithTags("a3", "io-foo-untagged", "https://a3.execute-api.us-east-1.amazonaws.com", "HTTP", map[string]string{"Owner": "team"}),
		}}},
	}
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return fake }}
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
	wantIDs := map[string]bool{"a1": true, "a2": true}
	for _, it := range items {
		if it.Service != "apigatewayv2_api" {
			t.Errorf("item.service=%q", it.Service)
		}
		if it.TFType != apigwV2APITFType {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
		if !wantIDs[it.ImportID] {
			t.Errorf("item.import_id=%q not in expected set", it.ImportID)
		}
	}
}

func TestAPIGWv2APIDiscoverByID_AcceptsBareID(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIClient{
		getByID: map[string]*apigatewayv2.GetApiOutput{
			"a1": {
				ApiId:        aws.String("a1"),
				Name:         aws.String("io-foo-orders"),
				ApiEndpoint:  aws.String("https://a1.execute-api.us-east-1.amazonaws.com"),
				ProtocolType: apigwv2types.ProtocolTypeHttp,
			},
		},
	}
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), "a1", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != apigwV2APITFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NativeIDs["api_id"] != "a1" {
		t.Errorf("NativeIDs[api_id]=%q", got.Identity.NativeIDs["api_id"])
	}
	if got.Identity.NativeIDs["protocol_type"] != "HTTP" {
		t.Errorf("NativeIDs[protocol_type]=%q", got.Identity.NativeIDs["protocol_type"])
	}
}

func TestAPIGWv2APIDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return &fakeAPIGWv2APIClient{} }}
	_, err := d.DiscoverByID(context.Background(), "missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestAPIGWv2APIDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &apigwV2APIDiscoverer{new: func(_ string) apigwV2APIClient { return &fakeAPIGWv2APIClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket",
		"id with space",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
