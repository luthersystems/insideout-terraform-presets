package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	retypes "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
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
//
// The expected count is computed from the registry rather than
// hard-coded: that way adding a new importable AWS type to the
// registry (without touching this test's fixture) doesn't require a
// numeric edit here. The fixture intentionally seeds rows of every
// shape — fixtureRows lists each ARN/type pair, and we count how many
// fixture rows map to a TF type currently in
// registry.SupportedDiscoverTypes("aws"). The remainder is the
// expected unsupported count.
func TestEnumerateUnsupported_FiltersSupportedTypes(t *testing.T) {
	t.Parallel()
	type fixtureRow struct {
		arn      string
		rxType   string
		tfType   string // the Terraform type the rx slug maps to
		region   string
		expected bool // whether this row should appear in unsupported.json
	}
	fixtureRows := []fixtureRow{
		{"arn:aws:sqs:us-east-1:123:io-queue", "sqs:queue", "aws_sqs_queue", "us-east-1", false},          // importable
		{"arn:aws:iam::123:role/io-role", "iam:role", "aws_iam_role", "us-east-1", false},                 // importable
		{"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", "ec2:vpc", "aws_vpc", "us-east-1", true},                // unsupported
		{"arn:aws:rds:us-east-1:123:cluster:my-clu", "rds:cluster", "aws_rds_cluster", "us-east-1", true}, // unsupported
		{"arn:aws:eks:us-east-1:123:cluster/my-eks", "eks:cluster", "aws_eks_cluster", "us-east-1", true}, // unsupported
	}
	// supportedSet is the live registry — if a fixture row's tfType
	// joins the registry in the future, the row's `expected` flag must
	// flip too. We sanity-check that here instead of trusting the
	// hand-written `expected` column.
	supportedSet := make(map[string]struct{})
	for _, t := range registry.SupportedDiscoverTypes("aws") {
		supportedSet[t] = struct{}{}
	}
	expectedUnsupported := 0
	expectedTypes := map[string]bool{}
	resources := make([]retypes.Resource, 0, len(fixtureRows))
	for _, row := range fixtureRows {
		_, isSupported := supportedSet[row.tfType]
		want := !isSupported
		if want != row.expected {
			t.Fatalf("fixture drift: row %q (tfType=%q) annotated expected=%v but registry says supported=%v — update the fixture's expected column",
				row.arn, row.tfType, row.expected, isSupported)
		}
		if want {
			expectedUnsupported++
			expectedTypes[row.tfType] = true
		}
		resources = append(resources, rxResource(row.arn, row.rxType, row.region))
	}
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{"us-east-1": resources},
	}
	got, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1"},
		Searcher: fake,
	}, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != expectedUnsupported {
		t.Fatalf("got %d unsupported rows, want %d (computed from registry). rows=%+v",
			len(got), expectedUnsupported, got)
	}
	for _, r := range got {
		if !expectedTypes[r.Type] {
			t.Errorf("row Type=%q not in expected unsupported set %v", r.Type, expectedTypes)
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

// TestEnumerateUnsupported_ImportableRowsAreFiltered pins the
// registry-subtraction contract for each row in
// awsUnsupportedTFTypeByResourceType whose mapped TF type is in the
// live registry: the row produces zero entries in unsupported.json
// (the picker reads it from imported.json instead). A regression
// that dropped the registry-subtract step would surface every
// importable row here as a non-empty `got`.
func TestEnumerateUnsupported_ImportableRowsAreFiltered(t *testing.T) {
	t.Parallel()
	supportedSet := make(map[string]struct{})
	for _, t := range registry.SupportedDiscoverTypes("aws") {
		supportedSet[t] = struct{}{}
	}
	for resourceType, wantTF := range awsTFTypeByResourceType {
		if _, importable := supportedSet[wantTF]; !importable {
			continue
		}
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
			if len(got) != 0 {
				t.Errorf("ResourceType=%q maps to importable TF type %q; want 0 rows, got %d (registry-subtract dropped)",
					resourceType, wantTF, len(got))
			}
		})
	}
}

// TestEnumerateUnsupported_UnimportableRowsThreadTFType walks the
// awsUnsupportedTFTypeByResourceType map for entries whose mapped TF
// type is NOT in the live registry — i.e. the rows that legitimately
// land in unsupported.json — and asserts each round-trips through
// enumerateUnsupportedAWS to its mapped Terraform type in
// UnsupportedResource.Type. A regression that transposed two columns
// of the map (e.g. mapping ec2:vpc → aws_subnet) would surface here.
func TestEnumerateUnsupported_UnimportableRowsThreadTFType(t *testing.T) {
	t.Parallel()
	supportedSet := make(map[string]struct{})
	for _, t := range registry.SupportedDiscoverTypes("aws") {
		supportedSet[t] = struct{}{}
	}
	for resourceType, wantTF := range awsTFTypeByResourceType {
		if _, importable := supportedSet[wantTF]; importable {
			continue
		}
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
			if len(got) != 1 {
				t.Fatalf("ResourceType=%q (TF=%q): got %d rows, want exactly 1", resourceType, wantTF, len(got))
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

// TestEnumerateUnsupported_PopulatesGroup pins the (#297) Category
// wire-through: every emitted UnsupportedResource carries a non-empty
// Group when its Type is in the categorized set, and an empty Group
// for unmapped Resource Explorer slugs (so the picker's "Other"
// fallback fires).
//
// We sample three categorized rows (one per main category cluster the
// AWS lookup table covers — Network Security, Compute, Data Storage)
// plus one unmapped slug. A regression that wired the wrong
// category map (or forgot to call imported.Category at all) surfaces
// here.
func TestEnumerateUnsupported_PopulatesGroup(t *testing.T) {
	t.Parallel()
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{
			"us-east-1": {
				rxResource("arn:aws:ec2:us-east-1:123:vpc/vpc-abc", "ec2:vpc", "us-east-1"),
				rxResource("arn:aws:eks:us-east-1:123:cluster/c", "eks:cluster", "us-east-1"),
				rxResource("arn:aws:rds:us-east-1:123:cluster:rds-c", "rds:cluster", "us-east-1"),
				rxResource("arn:aws:newservice:us-east-1:123:thing/x", "newservice:thing", "us-east-1"),
			},
		},
	}
	got, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:  []string{"us-east-1"},
		Searcher: fake,
	}, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	// Walk the slice directly: the previous map-keyed assertion folded
	// every Type=="" row onto a single key, hiding any case where two
	// unmapped slugs collided. Find each expected row by Type and
	// assert Group on the matching entry; for the unmapped row, walk
	// for the Type=="" entry and assert Group=="".
	wantGroup := map[string]string{
		"aws_vpc":         "Network Security",
		"aws_eks_cluster": "Virtual Machines",
		"aws_rds_cluster": "Data Storage",
	}
	for typ, want := range wantGroup {
		var found *UnsupportedResource
		for i := range got {
			if got[i].Type == typ {
				found = &got[i]
				break
			}
		}
		if found == nil {
			t.Errorf("type %q not found in emitted rows %+v", typ, got)
			continue
		}
		if found.Group != want {
			t.Errorf("Group for %q = %q, want %q", typ, found.Group, want)
		}
	}
	// Unmapped slug: there must be a row with Type=="" whose Group is
	// also "" (Category("") returns ""). Asserting on the slice
	// directly defends against the previous map-keyed shape, which
	// silently passed if the Type=="" row was missing entirely.
	var unmapped *UnsupportedResource
	for i := range got {
		if got[i].Type == "" {
			unmapped = &got[i]
			break
		}
	}
	if unmapped == nil {
		t.Fatalf("no row with Type==\"\" emitted; unmapped Resource Explorer slug must still surface (rows=%+v)", got)
	}
	if unmapped.Group != "" {
		t.Errorf("Group for Type==\"\" = %q, want \"\" (unmapped slug → no category)", unmapped.Group)
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
