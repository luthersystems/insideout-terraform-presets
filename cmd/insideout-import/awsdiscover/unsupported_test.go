package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	retypes "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
)

// fakeResourceExplorerSearcher is the unit-test seam for the AWS
// Resource Explorer surface. Tests configure `byRegion` (canned
// responses keyed on region) and an optional forced `err`.
type fakeResourceExplorerSearcher struct {
	byRegion map[string][]retypes.Resource
	err      error

	// callsByRegion captures one entry per region the caller asked for,
	// in invocation order, so multi-region tests can assert per-region
	// threading.
	callsByRegion []string
}

func (f *fakeResourceExplorerSearcher) Search(_ context.Context, region, _ string) ([]retypes.Resource, error) {
	f.callsByRegion = append(f.callsByRegion, region)
	if f.err != nil {
		return nil, f.err
	}
	return f.byRegion[region], nil
}

// rxResource constructs a Resource Explorer types.Resource with the
// fields enumerateUnsupportedAWS reads.
func rxResource(arn, resourceType, region string) retypes.Resource {
	return retypes.Resource{
		Arn:          aws.String(arn),
		ResourceType: aws.String(resourceType),
		Region:       aws.String(region),
	}
}

// TestEnumerateUnsupported_FiltersSupportedTypes pins the registry-
// subtraction contract: a fake returning a mix of importable +
// unimportable rows yields only the unimportable subset. The
// importable types come from registry.SupportedDiscoverTypes("aws"),
// so this test exercises the live registry — a regression that drops
// the subtract step would surface every importable row in
// unsupported.json (the picker would then duplicate).
func TestEnumerateUnsupported_FiltersSupportedTypes(t *testing.T) {
	t.Parallel()
	// 5 results: 2 are importable (aws_sqs_queue, aws_iam_role), 3 are
	// not (aws_vpc, aws_rds_cluster, aws_eks_cluster).
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{
			"us-east-1": {
				rxResource("arn:aws:sqs:us-east-1:123:io-queue", "sqs:queue", "us-east-1"),
				rxResource("arn:aws:iam::123:role/io-role", "iam:role", "us-east-1"),
				rxResource("arn:aws:ec2:us-east-1:123:vpc/vpc-abc", "ec2:vpc", "us-east-1"),
				rxResource("arn:aws:rds:us-east-1:123:cluster:my-clu", "rds:cluster", "us-east-1"),
				rxResource("arn:aws:eks:us-east-1:123:cluster/my-eks", "eks:cluster", "us-east-1"),
			},
		},
	}
	// Note: the importable types depend on the registry. Per the
	// current registry sqs:queue → aws_sqs_queue is importable;
	// iam:role → aws_iam_role is importable. ec2:vpc, rds:cluster,
	// eks:cluster are not (and they're in the lookup map). So 3 rows
	// expected.
	got, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1"},
		Searcher: fake,
	}, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d unsupported rows, want 3 (5 results minus 2 importable). rows=%+v", len(got), got)
	}
	wantTypes := map[string]bool{"aws_vpc": true, "aws_rds_cluster": true, "aws_eks_cluster": true}
	for _, r := range got {
		if !wantTypes[r.Type] {
			t.Errorf("row Type=%q not in expected unsupported set %v", r.Type, wantTypes)
		}
	}
}

// TestEnumerateUnsupported_MultiRegion pins per-region threading: each
// region in args.Regions produces one Search call, and the resulting
// rows carry the right Region field.
func TestEnumerateUnsupported_MultiRegion(t *testing.T) {
	t.Parallel()
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{
			"us-east-1": {rxResource("arn:aws:ec2:us-east-1:123:vpc/vpc-east", "ec2:vpc", "us-east-1")},
			"eu-west-1": {rxResource("arn:aws:ec2:eu-west-1:123:vpc/vpc-west", "ec2:vpc", "eu-west-1")},
		},
	}
	got, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1", "eu-west-1"},
		Searcher: fake,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (one per region)", len(got))
	}
	if len(fake.callsByRegion) != 2 || fake.callsByRegion[0] != "us-east-1" || fake.callsByRegion[1] != "eu-west-1" {
		t.Errorf("callsByRegion=%v, want [us-east-1 eu-west-1]", fake.callsByRegion)
	}
	regions := map[string]bool{got[0].Region: true, got[1].Region: true}
	if !regions["us-east-1"] || !regions["eu-west-1"] {
		t.Errorf("got regions %v, want both us-east-1 and eu-west-1", regions)
	}
}

// TestEnumerateUnsupported_TagsPassThrough is a placeholder pinning
// the documented contract: AWS Resource Explorer's Resource shape
// carries no inline tags map (Properties is type-specific and not
// unmarshaled), so emitted rows have Tags=nil. A regression that
// silently invented a Tags map (e.g. by reading a non-tag Property as
// tags) would fail this test — Tags must stay nil until a fanout opt-
// in lands.
func TestEnumerateUnsupported_TagsPassThrough(t *testing.T) {
	t.Parallel()
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{
			"us-east-1": {rxResource("arn:aws:ec2:us-east-1:123:vpc/vpc-abc", "ec2:vpc", "us-east-1")},
		},
	}
	got, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1"},
		Searcher: fake,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0].Tags != nil {
		t.Errorf("Tags=%v, want nil (Resource Explorer surface has no inline tags today)", got[0].Tags)
	}
}

// TestEnumerateUnsupported_TFTypeMappedFromResourceType walks the
// awsUnsupportedTFTypeByResourceType map and asserts each entry round-
// trips through enumerateUnsupportedAWS to its mapped Terraform type
// in UnsupportedResource.Type. A regression that transposed two
// columns of the map (e.g. mapping ec2:vpc → aws_subnet) would surface
// here.
func TestEnumerateUnsupported_TFTypeMappedFromResourceType(t *testing.T) {
	t.Parallel()
	for resourceType, wantTF := range awsTFTypeByResourceType {
		resourceType := resourceType
		wantTF := wantTF
		t.Run(resourceType, func(t *testing.T) {
			t.Parallel()
			fake := &fakeResourceExplorerSearcher{
				byRegion: map[string][]retypes.Resource{
					"us-east-1": {rxResource("arn:aws:test:us-east-1:123:thing/x", resourceType, "us-east-1")},
				},
			}
			got, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
				Regions:  []string{"us-east-1"},
				Searcher: fake,
			}, "")
			if err != nil {
				t.Fatal(err)
			}
			// Some rows in the map MAY map to importable types; in that case
			// the row is filtered out. Skip those rather than fail.
			if len(got) == 0 {
				t.Skipf("resource type %s maps to importable TF type %s; filtered out by registry-subtraction", resourceType, wantTF)
			}
			if got[0].Type != wantTF {
				t.Errorf("Type=%q, want %q for ResourceType=%q", got[0].Type, wantTF, resourceType)
			}
		})
	}
}

// TestEnumerateUnsupported_UnknownResourceTypePreservesEmpty pins the
// fall-through contract: an unknown ResourceType slug emits a row with
// Type="" and the slug in Name (so the picker can still surface it
// under "Other"). A regression that errored on unknown slugs would
// drop rows the operator actually has in their account.
func TestEnumerateUnsupported_UnknownResourceTypePreservesEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{
			"us-east-1": {rxResource("arn:aws:newservice:us-east-1:123:thing/x", "newservice:thing", "us-east-1")},
		},
	}
	got, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1"},
		Searcher: fake,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (unknown ResourceType must still pass through)", len(got))
	}
	if got[0].Type != "" {
		t.Errorf("Type=%q, want empty for unknown ResourceType", got[0].Type)
	}
	if got[0].Name != "x" {
		// The trailing path segment of the ARN; on a malformed ARN we
		// fall back to the resource type slug.
		t.Errorf("Name=%q, want %q (trailing ARN segment)", got[0].Name, "x")
	}
}

// TestEnumerateUnsupported_ResourceExplorerErrorIsReturned pins error
// propagation: a generic Search failure surfaces through to the
// caller (which decides between fatal and warn). The CLI wraps the
// returned error in a stderr WARN; this test only asserts the value
// is returned, not the wrapping.
func TestEnumerateUnsupported_ResourceExplorerErrorIsReturned(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("transient: connection reset")
	fake := &fakeResourceExplorerSearcher{
		err: wantErr,
	}
	_, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1"},
		Searcher: fake,
	}, "")
	if err == nil {
		t.Fatal("err=nil, want error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("returned err=%v, want wrap of %v", err, wantErr)
	}
}

// TestEnumerateUnsupported_ResourceExplorerNotConfigured pins the
// soft-failure marker: when the underlying Search error message hints
// at "no default view", the returned error wraps
// errResourceExplorerNotConfigured so the CLI's
// IsResourceExplorerNotConfigured branch fires.
func TestEnumerateUnsupported_ResourceExplorerNotConfigured(t *testing.T) {
	t.Parallel()
	fake := &fakeResourceExplorerSearcher{
		err: errors.New("ResourceNotFoundException: there is no default view for this region"),
	}
	_, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1"},
		Searcher: fake,
	}, "")
	if err == nil {
		t.Fatal("err=nil, want error")
	}
	if !IsResourceExplorerNotConfigured(err) {
		t.Errorf("err=%v not flagged as ResourceExplorerNotConfigured; CLI's soft-fail branch will mis-route", err)
	}
}

// TestEnumerateUnsupported_NilSearcherIsFatal pins the safety net: a
// nil Searcher errors out at the top of EnumerateUnsupported rather
// than panicking deep in the call stack. Tests that forget to wire a
// fake see this error.
func TestEnumerateUnsupported_NilSearcherIsFatal(t *testing.T) {
	t.Parallel()
	_, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions: []string{"us-east-1"},
	}, "")
	if err == nil {
		t.Fatal("err=nil, want explicit error")
	}
}

// TestEnumerateUnsupported_EmitsServiceStartFinishPerRegion pins the
// progress-event contract: one (service_start, service_finish) bracket
// per region, plus one item_found per emitted row. Mirrors the per-
// service progress tests added in #295.
func TestEnumerateUnsupported_EmitsServiceStartFinishPerRegion(t *testing.T) {
	t.Parallel()
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{
			"us-east-1": {
				rxResource("arn:aws:ec2:us-east-1:123:vpc/vpc-east-1", "ec2:vpc", "us-east-1"),
				rxResource("arn:aws:ec2:us-east-1:123:vpc/vpc-east-2", "ec2:vpc", "us-east-1"),
			},
			"eu-west-1": {
				rxResource("arn:aws:ec2:eu-west-1:123:vpc/vpc-west-1", "ec2:vpc", "eu-west-1"),
			},
		},
	}
	rec := &recordingEmitter{}
	if _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1", "eu-west-1"},
		Searcher: fake,
		Emitter:  rec,
	}, ""); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	items := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != "unsupported" {
				t.Errorf("service_start.service=%q, want unsupported", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != "unsupported" {
				t.Errorf("service_finish.service=%q, want unsupported", e.Service)
			}
			finishes[e.Region]++
		case "item_found":
			items[e.Region]++
		}
	}
	if starts["us-east-1"] != 1 || starts["eu-west-1"] != 1 {
		t.Errorf("starts=%v, want one per region", starts)
	}
	if finishes["us-east-1"] != 1 || finishes["eu-west-1"] != 1 {
		t.Errorf("finishes=%v, want one per region", finishes)
	}
	if items["us-east-1"] != 2 || items["eu-west-1"] != 1 {
		t.Errorf("items=%v, want {us-east-1:2, eu-west-1:1}", items)
	}
}

// TestAWSResourceNameFromARN_TrailingSegment pins the display-name
// extraction across ARN shape variants Resource Explorer hands back.
func TestAWSResourceNameFromARN_TrailingSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		arn  string
		want string
	}{
		{"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", "vpc-abc"},
		{"arn:aws:rds:us-east-1:123:cluster:my-cluster", "my-cluster"},
		{"arn:aws:s3:::my-bucket", "my-bucket"},
		{"not-an-arn", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := awsResourceNameFromARN(tc.arn)
		if got != tc.want {
			t.Errorf("awsResourceNameFromARN(%q)=%q, want %q", tc.arn, got, tc.want)
		}
	}
}
