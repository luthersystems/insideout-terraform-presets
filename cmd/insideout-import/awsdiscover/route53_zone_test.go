package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// errRoute53ZoneSeed is the package-level sentinel for route53_zone
// error propagation — see vpc_test.go for the contract.
var errRoute53ZoneSeed = errors.New("AccessDenied")

type fakeRoute53ZoneClient struct {
	pages []route53.ListHostedZonesOutput
	calls []route53.ListHostedZonesInput
	err   error

	getByID  map[string]*route53types.HostedZone
	getErr   error
	getCalls []string

	tagsByID  map[string][]route53types.Tag
	tagsErr   error
	tagsCalls []string
}

func (f *fakeRoute53ZoneClient) ListHostedZones(_ context.Context, in *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &route53.ListHostedZonesOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeRoute53ZoneClient) GetHostedZone(_ context.Context, in *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	id := aws.ToString(in.Id)
	f.getCalls = append(f.getCalls, id)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if hz, ok := f.getByID[id]; ok {
		return &route53.GetHostedZoneOutput{HostedZone: hz}, nil
	}
	return nil, &route53types.NoSuchHostedZone{}
}

func (f *fakeRoute53ZoneClient) ListTagsForResource(_ context.Context, in *route53.ListTagsForResourceInput, _ ...func(*route53.Options)) (*route53.ListTagsForResourceOutput, error) {
	id := aws.ToString(in.ResourceId)
	f.tagsCalls = append(f.tagsCalls, id)
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if t, ok := f.tagsByID[id]; ok {
		return &route53.ListTagsForResourceOutput{ResourceTagSet: &route53types.ResourceTagSet{Tags: t}}, nil
	}
	// Return non-nil but empty ResourceTagSet (canonical empty shape).
	return &route53.ListTagsForResourceOutput{ResourceTagSet: &route53types.ResourceTagSet{}}, nil
}

// hostedZone helper with the SDK-returned "/hostedzone/" prefix on Id
// and a trailing dot on Name so tests exercise the strip logic.
func hostedZone(id, name string, private bool) route53types.HostedZone {
	hz := route53types.HostedZone{
		Id:     aws.String("/hostedzone/" + id),
		Name:   aws.String(name + "."),
		Config: &route53types.HostedZoneConfig{PrivateZone: private},
	}
	return hz
}

func TestRoute53ZoneDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{
			pages: []route53.ListHostedZonesOutput{
				{HostedZones: []route53types.HostedZone{
					hostedZone("Z01ABCDEFGHIJKLMNOPQR", "io-foo.example.com", false),
					hostedZone("Z02WXYZ12345678901234", "io-foo-internal.private", true),
				}},
			},
			tagsByID: map[string][]route53types.Tag{
				"Z01ABCDEFGHIJKLMNOPQR": {
					{Key: aws.String("Project"), Value: aws.String("io-foo")},
					{Key: aws.String("Env"), Value: aws.String("prod")},
				},
			},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != "aws_route53_zone" {
			t.Errorf("Type=%q, want aws_route53_zone", ir.Identity.Type)
		}
		if ir.Identity.Region != "" {
			t.Errorf("Region=%q, want empty (route53 is global)", ir.Identity.Region)
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
	}
	// Sorted by zone ID.
	if got[0].Identity.ImportID != "Z01ABCDEFGHIJKLMNOPQR" {
		t.Errorf("first ImportID=%q, want Z01ABCDEFGHIJKLMNOPQR (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NameHint != "io-foo.example.com" {
		t.Errorf("first NameHint=%q, want io-foo.example.com (trailing dot stripped)", got[0].Identity.NameHint)
	}
	if got[0].Identity.NativeIDs["hosted_zone_id"] != "Z01ABCDEFGHIJKLMNOPQR" {
		t.Errorf("NativeIDs[hosted_zone_id]=%q", got[0].Identity.NativeIDs["hosted_zone_id"])
	}
	if got[0].Identity.NativeIDs["name"] != "io-foo.example.com" {
		t.Errorf("NativeIDs[name]=%q", got[0].Identity.NativeIDs["name"])
	}
	if _, ok := got[0].Identity.NativeIDs["private_zone"]; ok {
		t.Errorf("NativeIDs[private_zone] unexpectedly set on public zone")
	}
	// Private-zone marker on the second zone.
	if got[1].Identity.NativeIDs["private_zone"] != "true" {
		t.Errorf("private NativeIDs[private_zone]=%q, want \"true\"", got[1].Identity.NativeIDs["private_zone"])
	}
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestRoute53ZoneDiscover_TrimsHostedZonePrefix(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{
			pages: []route53.ListHostedZonesOutput{{HostedZones: []route53types.HostedZone{
				hostedZone("Z00DEADBEEF1234567890", "io-foo.example.com", false),
			}}},
		}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	// ImportID must be the bare ID (no leading "/hostedzone/").
	if got[0].Identity.ImportID != "Z00DEADBEEF1234567890" {
		t.Errorf("ImportID=%q, want bare zone ID without /hostedzone/ prefix", got[0].Identity.ImportID)
	}
	if got[0].Identity.NativeIDs["hosted_zone_id"] != "Z00DEADBEEF1234567890" {
		t.Errorf("NativeIDs[hosted_zone_id]=%q, want bare zone ID", got[0].Identity.NativeIDs["hosted_zone_id"])
	}
}

func TestRoute53ZoneDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	truthy := true
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{pages: []route53.ListHostedZonesOutput{
			{HostedZones: []route53types.HostedZone{hostedZone("Z00AAAAAA1111111111AAA", "io-foo-a.example.com", false)}, IsTruncated: truthy, NextMarker: aws.String("Z00BBBBBB1111111111BBB")},
			{HostedZones: []route53types.HostedZone{hostedZone("Z00BBBBBB1111111111BBB", "io-foo-b.example.com", false)}, IsTruncated: truthy, NextMarker: aws.String("Z00CCCCCC1111111111CCC")},
			{HostedZones: []route53types.HostedZone{hostedZone("Z00CCCCCC1111111111CCC", "io-foo-c.example.com", false)}}, // terminal: IsTruncated=false zero value
		}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
}

func TestRoute53ZoneDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{pages: []route53.ListHostedZonesOutput{
			{HostedZones: []route53types.HostedZone{
				hostedZone("Z00FOO0000000000000001", "io-foo.example.com", false),
				hostedZone("Z00BAR0000000000000002", "other-team.example.com", false),
				hostedZone("Z00FOO0000000000000003", "io-foo-internal.private", true),
			}},
		}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (only io-foo-prefixed zones)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NameHint == "other-team.example.com" {
			t.Errorf("non-io-foo zone leaked through filter: %v", ir.Identity)
		}
	}
}

func TestRoute53ZoneDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{pages: []route53.ListHostedZonesOutput{
			{HostedZones: []route53types.HostedZone{
				hostedZone("Z00AAA0000000000000001", "a.example.com", false),
				hostedZone("Z00BBB0000000000000002", "b.example.com", false),
			}},
		}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (no project filter)", len(got))
	}
}

func TestRoute53ZoneDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{err: errRoute53ZoneSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errRoute53ZoneSeed) {
		t.Errorf("err=%v, want errors.Is(err, errRoute53ZoneSeed)", err)
	}
}

func TestRoute53ZoneDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{
			pages: []route53.ListHostedZonesOutput{{HostedZones: []route53types.HostedZone{
				hostedZone("Z00PROD000000000000001", "io-foo.example.com", false),
				hostedZone("Z00STAG000000000000002", "io-foo-staging.example.com", false),
			}}},
			tagsByID: map[string][]route53types.Tag{
				"Z00PROD000000000000001": {{Key: aws.String("env"), Value: aws.String("prod")}},
				"Z00STAG000000000000002": {{Key: aws.String("env"), Value: aws.String("staging")}},
			},
		}
	}}
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
		t.Fatalf("len=%d, want 1 (only env=prod zone should pass)", len(got))
	}
	if got[0].Identity.ImportID != "Z00PROD000000000000001" {
		t.Errorf("ImportID=%q, want Z00PROD000000000000001", got[0].Identity.ImportID)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestRoute53ZoneDiscover_GlobalServiceEmitsOnceWithEmptyRegion(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{pages: []route53.ListHostedZonesOutput{
			{HostedZones: []route53types.HostedZone{hostedZone("Z00ONE0000000000000001", "io-foo.example.com", false)}},
		}}
	}}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		// Multiple regions provided; route53 is global so only ONE
		// service_start should fire and Region must be empty.
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, e := range rec.snapshot() {
		if e.Kind == "service_start" {
			starts++
			if e.Region != "" {
				t.Errorf("service_start Region=%q, want empty (route53 is global)", e.Region)
			}
			if e.Service != "route53_zone" {
				t.Errorf("service_start Service=%q, want route53_zone", e.Service)
			}
		}
	}
	if starts != 1 {
		t.Errorf("service_start count=%d, want 1 (global service emits once regardless of Regions)", starts)
	}
}

func TestRoute53ZoneDiscover_EmitsServiceStartFinish_Once(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{pages: []route53.ListHostedZonesOutput{
			{HostedZones: []route53types.HostedZone{hostedZone("Z00ONE0000000000000001", "io-foo.example.com", false)}},
		}}
	}}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	events := rec.snapshot()
	var startIdx, finishIdx int = -1, -1
	for i, e := range events {
		switch e.Kind {
		case "service_start":
			if startIdx != -1 {
				t.Errorf("multiple service_start events emitted; only one expected for global service")
			}
			startIdx = i
			if e.Region != "" {
				t.Errorf("service_start Region=%q, want empty", e.Region)
			}
		case "service_finish":
			if finishIdx != -1 {
				t.Errorf("multiple service_finish events emitted; only one expected for global service")
			}
			finishIdx = i
			if e.Region != "" {
				t.Errorf("service_finish Region=%q, want empty", e.Region)
			}
		}
	}
	if startIdx == -1 || finishIdx == -1 {
		t.Fatalf("missing service_start or service_finish: events=%+v", events)
	}
	if startIdx >= finishIdx {
		t.Errorf("start at %d >= finish at %d", startIdx, finishIdx)
	}
}

func TestRoute53ZoneDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"Z00AAA0000000000000001", "Z00BBB0000000000000002", "Z00CCC0000000000000003"}
	hzs := make([]route53types.HostedZone, 0, len(ids))
	for _, id := range ids {
		hzs = append(hzs, hostedZone(id, "io-foo-"+id+".example.com", false))
	}
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		return &fakeRoute53ZoneClient{pages: []route53.ListHostedZonesOutput{{HostedZones: hzs}}}
	}}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
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
		t.Errorf("item_found count=%d, want %d", len(items), len(got))
	}
	for i, it := range items {
		if it.Service != "route53_zone" {
			t.Errorf("item %d: service=%q, want route53_zone", i, it.Service)
		}
		if it.TFType != "aws_route53_zone" {
			t.Errorf("item %d: tf_type=%q, want aws_route53_zone", i, it.TFType)
		}
		if it.Region != "" {
			t.Errorf("item %d: region=%q, want empty", i, it.Region)
		}
	}
}

func TestRoute53ZoneDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		hz := hostedZone("Z01ABCDEFGHIJKLMNOPQR", "io-foo.example.com", false)
		return &fakeRoute53ZoneClient{getByID: map[string]*route53types.HostedZone{"Z01ABCDEFGHIJKLMNOPQR": &hz}}
	}}
	got, err := d.DiscoverByID(context.Background(), "Z01ABCDEFGHIJKLMNOPQR", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_route53_zone" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "Z01ABCDEFGHIJKLMNOPQR" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo.example.com" {
		t.Errorf("NameHint=%q, want io-foo.example.com", got.Identity.NameHint)
	}
	if got.Identity.Region != "" {
		t.Errorf("Region=%q, want empty (route53 is global)", got.Identity.Region)
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestRoute53ZoneDiscoverByID_AcceptsPrefixedID(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient {
		hz := hostedZone("Z01ABCDEFGHIJKLMNOPQR", "io-foo.example.com", false)
		return &fakeRoute53ZoneClient{getByID: map[string]*route53types.HostedZone{"Z01ABCDEFGHIJKLMNOPQR": &hz}}
	}}
	// Operator may pass the path-prefixed form Route 53 itself returns.
	got, err := d.DiscoverByID(context.Background(), "/hostedzone/Z01ABCDEFGHIJKLMNOPQR", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "Z01ABCDEFGHIJKLMNOPQR" {
		t.Errorf("ImportID=%q, want bare zone ID", got.Identity.ImportID)
	}
}

func TestRoute53ZoneDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient { return &fakeRoute53ZoneClient{} }}
	_, err := d.DiscoverByID(context.Background(), "Z00MISSING0000000001", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestRoute53ZoneDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &route53ZoneDiscoverer{new: func(_ string) route53ZoneClient { return &fakeRoute53ZoneClient{} }}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // not a route53 ARN
		"arn:aws:route53:::hostedzone/Z01ABCDEFGHIJKLMNOPQR", // ARN-shape — route53 doesn't have an importable ARN form
		"/hostedzone/",           // empty after strip
		"some thing with spaces", // invalid chars
		"/hostedzone/Z01/extra",  // path traversal in payload
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
