package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// errCloudFrontDistributionSeed is the package-level sentinel for
// cloudfront_distribution error propagation — see vpc_test.go for the
// contract.
var errCloudFrontDistributionSeed = errors.New("AccessDenied")

type fakeCloudFrontDistributionClient struct {
	pages []cloudfront.ListDistributionsOutput
	calls []cloudfront.ListDistributionsInput
	err   error

	getByID  map[string]*cftypes.Distribution
	getErr   error
	getCalls []string

	tagsByARN map[string][]cftypes.Tag
	tagsErr   error
	tagsCalls []string
}

func (f *fakeCloudFrontDistributionClient) ListDistributions(_ context.Context, in *cloudfront.ListDistributionsInput, _ ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
	f.calls = append(f.calls, *in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.pages) {
		return &cloudfront.ListDistributionsOutput{}, nil
	}
	return &f.pages[idx], nil
}

func (f *fakeCloudFrontDistributionClient) GetDistribution(_ context.Context, in *cloudfront.GetDistributionInput, _ ...func(*cloudfront.Options)) (*cloudfront.GetDistributionOutput, error) {
	id := aws.ToString(in.Id)
	f.getCalls = append(f.getCalls, id)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if dst, ok := f.getByID[id]; ok {
		return &cloudfront.GetDistributionOutput{Distribution: dst}, nil
	}
	return nil, &cftypes.NoSuchDistribution{}
}

func (f *fakeCloudFrontDistributionClient) ListTagsForResource(_ context.Context, in *cloudfront.ListTagsForResourceInput, _ ...func(*cloudfront.Options)) (*cloudfront.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.Resource)
	f.tagsCalls = append(f.tagsCalls, arn)
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	items := f.tagsByARN[arn]
	return &cloudfront.ListTagsForResourceOutput{Tags: &cftypes.Tags{Items: items}}, nil
}

// distributionSummary builds a minimal DistributionSummary populating
// only the fields the discoverer reads.
func distributionSummary(id, comment string, aliases []string) cftypes.DistributionSummary {
	arn := "arn:aws:cloudfront::123456789012:distribution/" + id
	domain := id + ".cloudfront.net"
	s := cftypes.DistributionSummary{
		Id:         aws.String(id),
		ARN:        aws.String(arn),
		Comment:    aws.String(comment),
		DomainName: aws.String(domain),
	}
	if aliases != nil {
		s.Aliases = &cftypes.Aliases{Items: aliases}
	} else {
		s.Aliases = &cftypes.Aliases{}
	}
	return s
}

func TestCloudFrontDistributionDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{
			pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{
				Items: []cftypes.DistributionSummary{
					distributionSummary("E1U5RQF7T870K0", "io-foo cdn", []string{"cdn.io-foo.example.com"}),
					distributionSummary("E2TKCBW0F18ZRW", "io-foo internal", nil),
				},
			}}},
			tagsByARN: map[string][]cftypes.Tag{
				"arn:aws:cloudfront::123456789012:distribution/E1U5RQF7T870K0": {
					{Key: aws.String("Project"), Value: aws.String("io-foo")},
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
		if ir.Identity.Type != "aws_cloudfront_distribution" {
			t.Errorf("Type=%q, want aws_cloudfront_distribution", ir.Identity.Type)
		}
		if ir.Identity.Region != "" {
			t.Errorf("Region=%q, want empty (cloudfront is global)", ir.Identity.Region)
		}
		if ir.Identity.NativeIDs["arn"] == "" {
			t.Errorf("NativeIDs[arn] empty")
		}
		if ir.Identity.NativeIDs["domain_name"] == "" {
			t.Errorf("NativeIDs[domain_name] empty")
		}
	}
	// Sorted by distribution ID. E1U5RQF7T870K0 < E2TKCBW0F18ZRW.
	if got[0].Identity.ImportID != "E1U5RQF7T870K0" {
		t.Errorf("first ImportID=%q, want E1U5RQF7T870K0 (sorted)", got[0].Identity.ImportID)
	}
	if got[0].Identity.NativeIDs["primary_alias"] != "cdn.io-foo.example.com" {
		t.Errorf("NativeIDs[primary_alias]=%q", got[0].Identity.NativeIDs["primary_alias"])
	}
	if _, ok := got[1].Identity.NativeIDs["primary_alias"]; ok {
		t.Errorf("NativeIDs[primary_alias] unexpectedly set on no-aliases distribution")
	}
	if got[0].Identity.Tags["Project"] != "io-foo" {
		t.Errorf("Tags[Project]=%q, want io-foo", got[0].Identity.Tags["Project"])
	}
}

func TestCloudFrontDistributionDiscover_UsesCommentAsNameHint(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{
			Items: []cftypes.DistributionSummary{
				distributionSummary("E0WITHCOMMENT0001", "io-foo edge", nil),
				distributionSummary("E1NOCOMMENT000001", "", nil), // empty comment falls back to ID
			},
		}}}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].Identity.NameHint != "io-foo edge" {
		t.Errorf("first NameHint=%q, want io-foo edge (Comment as name)", got[0].Identity.NameHint)
	}
	// Second distribution has empty Comment — fall back to the ID.
	if got[1].Identity.NameHint != "E1NOCOMMENT000001" {
		t.Errorf("second NameHint=%q, want E1NOCOMMENT000001 (fallback to ID when Comment empty)", got[1].Identity.NameHint)
	}
}

func TestCloudFrontDistributionDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	truthy := true
	falsy := false
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{pages: []cloudfront.ListDistributionsOutput{
			{DistributionList: &cftypes.DistributionList{
				Items:       []cftypes.DistributionSummary{distributionSummary("E1AAAA0000000001", "io-foo a", nil)},
				IsTruncated: &truthy,
				NextMarker:  aws.String("E1BBBB0000000002"),
			}},
			{DistributionList: &cftypes.DistributionList{
				Items:       []cftypes.DistributionSummary{distributionSummary("E1BBBB0000000002", "io-foo b", nil)},
				IsTruncated: &truthy,
				NextMarker:  aws.String("E1CCCC0000000003"),
			}},
			{DistributionList: &cftypes.DistributionList{
				Items:       []cftypes.DistributionSummary{distributionSummary("E1CCCC0000000003", "io-foo c", nil)},
				IsTruncated: &falsy,
			}},
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

func TestCloudFrontDistributionDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{
			Items: []cftypes.DistributionSummary{
				distributionSummary("E1FOO00000000001", "io-foo cdn", nil),
				distributionSummary("E2BAR00000000002", "other-team cdn", nil),
				distributionSummary("E3FOO00000000003", "io-foo internal", nil),
			},
		}}}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (only io-foo-prefixed comments)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NameHint == "other-team cdn" {
			t.Errorf("non-io-foo distribution leaked through filter: %v", ir.Identity)
		}
	}
}

func TestCloudFrontDistributionDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{
			Items: []cftypes.DistributionSummary{
				distributionSummary("E1AAAA0000000001", "a cdn", nil),
				distributionSummary("E2BBBB0000000002", "b cdn", nil),
			},
		}}}}
	}}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (no project filter)", len(got))
	}
}

func TestCloudFrontDistributionDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{err: errCloudFrontDistributionSeed}
	}}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errCloudFrontDistributionSeed) {
		t.Errorf("err=%v, want errors.Is(err, errCloudFrontDistributionSeed)", err)
	}
}

func TestCloudFrontDistributionDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{
			pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{
				Items: []cftypes.DistributionSummary{
					distributionSummary("E1PROD0000000001", "io-foo prod", nil),
					distributionSummary("E2STAG0000000002", "io-foo staging", nil),
				},
			}}},
			tagsByARN: map[string][]cftypes.Tag{
				"arn:aws:cloudfront::123456789012:distribution/E1PROD0000000001": {{Key: aws.String("env"), Value: aws.String("prod")}},
				"arn:aws:cloudfront::123456789012:distribution/E2STAG0000000002": {{Key: aws.String("env"), Value: aws.String("staging")}},
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
		t.Fatalf("len=%d, want 1 (only env=prod distribution should pass)", len(got))
	}
	if got[0].Identity.ImportID != "E1PROD0000000001" {
		t.Errorf("ImportID=%q, want E1PROD0000000001", got[0].Identity.ImportID)
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q, want prod (filter+persist contract)", got[0].Identity.Tags["env"])
	}
}

func TestCloudFrontDistributionDiscover_GlobalServiceEmitsOnceWithEmptyRegion(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{
			Items: []cftypes.DistributionSummary{distributionSummary("E1ONE0000000001", "io-foo one", nil)},
		}}}}
	}}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		// Multiple regions provided; cloudfront is global so only ONE
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
				t.Errorf("service_start Region=%q, want empty (cloudfront is global)", e.Region)
			}
			if e.Service != "cloudfront_distribution" {
				t.Errorf("service_start Service=%q, want cloudfront_distribution", e.Service)
			}
		}
	}
	if starts != 1 {
		t.Errorf("service_start count=%d, want 1 (global service emits once regardless of Regions)", starts)
	}
}

func TestCloudFrontDistributionDiscover_EmitsServiceStartFinish_Once(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{
			Items: []cftypes.DistributionSummary{distributionSummary("E1ONE0000000001", "io-foo", nil)},
		}}}}
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
	startIdx, finishIdx := -1, -1
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

func TestCloudFrontDistributionDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	ids := []string{"E1AAA0000000001", "E1BBB0000000002", "E1CCC0000000003"}
	items := make([]cftypes.DistributionSummary, 0, len(ids))
	for _, id := range ids {
		items = append(items, distributionSummary(id, "io-foo "+id, nil))
	}
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{pages: []cloudfront.ListDistributionsOutput{{DistributionList: &cftypes.DistributionList{Items: items}}}}
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
	var emitted []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "item_found" {
			emitted = append(emitted, e)
		}
	}
	if len(emitted) != len(got) {
		t.Errorf("item_found count=%d, want %d", len(emitted), len(got))
	}
	for i, it := range emitted {
		if it.Service != "cloudfront_distribution" {
			t.Errorf("item %d: service=%q, want cloudfront_distribution", i, it.Service)
		}
		if it.TFType != "aws_cloudfront_distribution" {
			t.Errorf("item %d: tf_type=%q, want aws_cloudfront_distribution", i, it.TFType)
		}
		if it.Region != "" {
			t.Errorf("item %d: region=%q, want empty", i, it.Region)
		}
	}
}

func TestCloudFrontDistributionDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		dst := &cftypes.Distribution{
			Id:         aws.String("E1U5RQF7T870K0"),
			ARN:        aws.String("arn:aws:cloudfront::123456789012:distribution/E1U5RQF7T870K0"),
			DomainName: aws.String("d111.cloudfront.net"),
			DistributionConfig: &cftypes.DistributionConfig{
				Comment: aws.String("io-foo edge"),
				Aliases: &cftypes.Aliases{Items: []string{"cdn.io-foo.example.com"}},
			},
		}
		return &fakeCloudFrontDistributionClient{getByID: map[string]*cftypes.Distribution{"E1U5RQF7T870K0": dst}}
	}}
	got, err := d.DiscoverByID(context.Background(), "E1U5RQF7T870K0", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_cloudfront_distribution" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "E1U5RQF7T870K0" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NameHint != "io-foo edge" {
		t.Errorf("NameHint=%q, want io-foo edge", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["arn"] == "" {
		t.Error("NativeIDs[arn] empty")
	}
	if got.Identity.NativeIDs["primary_alias"] != "cdn.io-foo.example.com" {
		t.Errorf("NativeIDs[primary_alias]=%q", got.Identity.NativeIDs["primary_alias"])
	}
	if got.Identity.Region != "" {
		t.Errorf("Region=%q, want empty (cloudfront is global)", got.Identity.Region)
	}
	if got.Identity.Tags != nil {
		t.Errorf("Tags=%v, want nil from DiscoverByID", got.Identity.Tags)
	}
}

func TestCloudFrontDistributionDiscoverByID_AcceptsARN(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		dst := &cftypes.Distribution{
			Id:         aws.String("E1U5RQF7T870K0"),
			ARN:        aws.String("arn:aws:cloudfront::123456789012:distribution/E1U5RQF7T870K0"),
			DomainName: aws.String("d111.cloudfront.net"),
			DistributionConfig: &cftypes.DistributionConfig{
				Comment: aws.String("io-foo edge"),
			},
		}
		return &fakeCloudFrontDistributionClient{getByID: map[string]*cftypes.Distribution{"E1U5RQF7T870K0": dst}}
	}}
	got, err := d.DiscoverByID(context.Background(),
		"arn:aws:cloudfront::123456789012:distribution/E1U5RQF7T870K0", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "E1U5RQF7T870K0" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestCloudFrontDistributionDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{}
	}}
	_, err := d.DiscoverByID(context.Background(), "E0MISSING0000001", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestCloudFrontDistributionDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &cloudfrontDistributionDiscoverer{new: func(_ string) cloudfrontDistributionClient {
		return &fakeCloudFrontDistributionClient{}
	}}
	cases := []string{
		"",
		"arn:aws:s3:::a-bucket", // not cloudfront
		"arn:aws:cloudfront::123:streaming-distribution/EABC1234567890", // wrong cloudfront resource type
		"arn:aws:cloudfront::123:distribution/",                         // empty id
		"some thing with spaces",                                        // contains space
		"E1ID/with/slash",                                               // bare-shape but contains slash
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
