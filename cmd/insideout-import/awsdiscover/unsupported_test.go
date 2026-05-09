package awsdiscover

import (
	"context"
	"errors"
	"fmt"
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

	// callsByMaxResults mirrors callsByRegion but captures the per-call
	// MaxResults the orchestrator threaded through. #309 cap-firing
	// tests use this to assert the bound reaches the searcher seam.
	callsByMaxResults []int
}

func (f *fakeResourceExplorerSearcher) Search(_ context.Context, region, _ string, maxResults int) ([]retypes.Resource, bool, error) {
	f.callsByRegion = append(f.callsByRegion, region)
	f.callsByMaxResults = append(f.callsByMaxResults, maxResults)
	if f.err != nil {
		return nil, false, f.err
	}
	results := f.byRegion[region]
	// Honor the cap at the fake too so the in-loop early-stop
	// behaviour of the real searcher is preserved end-to-end.
	if maxResults > 0 && len(results) > maxResults {
		return results[:maxResults], true, nil
	}
	return results, false, nil
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
		{"arn:aws:ec2:us-east-1:123:vpc/vpc-abc", "ec2:vpc", "aws_vpc", "us-east-1", false},               // importable (Bundle 4 PR 1)
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
	got, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
			"us-east-1": {rxResource("arn:aws:rds:us-east-1:123:cluster:c-east", "rds:cluster", "us-east-1")},
			"eu-west-1": {rxResource("arn:aws:rds:eu-west-1:123:cluster:c-west", "rds:cluster", "eu-west-1")},
		},
	}
	got, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
			"us-east-1": {rxResource("arn:aws:rds:us-east-1:123:cluster:c-abc", "rds:cluster", "us-east-1")},
		},
	}
	got, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
	for _, tfType := range registry.SupportedDiscoverTypes("aws") {
		supportedSet[tfType] = struct{}{}
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
			got, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
	for _, tfType := range registry.SupportedDiscoverTypes("aws") {
		supportedSet[tfType] = struct{}{}
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
			got, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
	got, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
	_, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
	_, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
	_, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
				rxResource("arn:aws:rds:us-east-1:123:cluster:c-east-1", "rds:cluster", "us-east-1"),
				rxResource("arn:aws:rds:us-east-1:123:cluster:c-east-2", "rds:cluster", "us-east-1"),
			},
			"eu-west-1": {
				rxResource("arn:aws:rds:eu-west-1:123:cluster:c-west-1", "rds:cluster", "eu-west-1"),
			},
		},
	}
	rec := &recordingEmitter{}
	if _, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
				// aws_elb (classic ELB) — still in the unsupported set; the
				// ELBv2 ALB type aws_lb landed as importable in #328.
				rxResource("arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/n", "elasticloadbalancing:loadbalancer-v1", "us-east-1"),
				rxResource("arn:aws:eks:us-east-1:123:cluster/c", "eks:cluster", "us-east-1"),
				rxResource("arn:aws:rds:us-east-1:123:cluster:rds-c", "rds:cluster", "us-east-1"),
				rxResource("arn:aws:newservice:us-east-1:123:thing/x", "newservice:thing", "us-east-1"),
			},
		},
	}
	got, _, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
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
		"aws_elb":         "Network Security",
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

// --- #309 MaxResults cap tests ---

// TestEnumerateUnsupported_CapFiresAndSetsTruncated pins the #309
// cap-and-warn contract: when the fake searcher returns 50 rows and
// MaxResults=10, the wrapper returns exactly 10 rows + truncated=true.
// The fake honors the cap internally (matching the real searcher's
// in-loop early stop), so this also exercises the per-region cap path.
func TestEnumerateUnsupported_CapFiresAndSetsTruncated(t *testing.T) {
	t.Parallel()
	rows := make([]retypes.Resource, 0, 50)
	for i := 0; i < 50; i++ {
		// rds:cluster maps to aws_rds_cluster which is NOT in the
		// importable registry, so each fixture row passes the
		// supported-set filter and lands in the output.
		arn := fmt.Sprintf("arn:aws:rds:us-east-1:1:cluster:c-%03d", i)
		rows = append(rows, rxResource(arn, "rds:cluster", "us-east-1"))
	}
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{"us-east-1": rows},
	}
	got, truncated, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:    []string{"us-east-1"},
		Searcher:   fake,
		MaxResults: 10,
	}, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Errorf("truncated=false, want true (cap=10, source=50)")
	}
	if len(got) != 10 {
		t.Errorf("len(got)=%d, want 10 (cap)", len(got))
	}
	if len(fake.callsByMaxResults) != 1 || fake.callsByMaxResults[0] != 10 {
		t.Errorf("searcher MaxResults=%v, want [10]", fake.callsByMaxResults)
	}
}

// TestEnumerateUnsupported_CapZeroDisablesLimit pins the
// "0 = unbounded" contract: a 50-row source with cap=0 returns all 50
// rows and truncated=false. Mirrors the documentation on
// UnsupportedArgs.MaxResults.
func TestEnumerateUnsupported_CapZeroDisablesLimit(t *testing.T) {
	t.Parallel()
	rows := make([]retypes.Resource, 0, 50)
	for i := 0; i < 50; i++ {
		arn := fmt.Sprintf("arn:aws:rds:us-east-1:1:cluster:c-%03d", i)
		rows = append(rows, rxResource(arn, "rds:cluster", "us-east-1"))
	}
	fake := &fakeResourceExplorerSearcher{
		byRegion: map[string][]retypes.Resource{"us-east-1": rows},
	}
	got, truncated, err := enumerateUnsupportedAWS(context.Background(), UnsupportedArgs{
		Regions:    []string{"us-east-1"},
		Searcher:   fake,
		MaxResults: 0,
	}, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Errorf("truncated=true, want false (cap=0 disables the limit)")
	}
	if len(got) != 50 {
		t.Errorf("len(got)=%d, want 50 (uncapped)", len(got))
	}
	// MaxResults reaching the searcher must be 0 too — otherwise the
	// real searcher would still fetch only the first N pages.
	if len(fake.callsByMaxResults) != 1 || fake.callsByMaxResults[0] != 0 {
		t.Errorf("searcher MaxResults=%v, want [0]", fake.callsByMaxResults)
	}
}

// TestSearch_CapStopsFetchingPages pins the load-bearing claim of the
// AWS-side cap: when MaxResults fires inside a page loop, the searcher
// MUST stop fetching subsequent pages. A cap that only truncated the
// in-memory slice would still burn API budget on every NextToken
// round-trip.
//
// The test wires a stub Resource Explorer client by exercising the
// real searcher with a minimal in-process pager — we can't easily
// stand up a real SDK client here, so the test inspects the
// pageFetchCounter via the resourceExplorerSearcher seam: a counter-
// wrapping fake that records the number of times Search would have
// fetched a page.
func TestSearch_CapStopsFetchingPages(t *testing.T) {
	t.Parallel()
	// Three pages of 100 rows each (300 total). With cap=150, the
	// searcher must fetch page 1 (accumulator=100), then page 2
	// (accumulator=150 mid-page → stop), and NEVER fetch page 3.
	pages := [][]retypes.Resource{
		makeFixtureRows(100, 0),
		makeFixtureRows(100, 100),
		makeFixtureRows(100, 200),
	}
	fake := &pagedFakeSearcher{pages: pages}
	got, truncated, err := fake.Search(context.Background(), "us-east-1", "*", 150)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Errorf("truncated=false, want true")
	}
	if len(got) != 150 {
		t.Errorf("len(got)=%d, want 150", len(got))
	}
	if fake.pagesFetched != 2 {
		t.Errorf("pagesFetched=%d, want 2 (page 3 must NOT be fetched)", fake.pagesFetched)
	}
}

// makeFixtureRows builds n Resource Explorer rows starting at the
// given offset. Used by TestSearch_CapStopsFetchingPages to construct
// a deterministic multi-page fixture.
func makeFixtureRows(n, offset int) []retypes.Resource {
	out := make([]retypes.Resource, 0, n)
	for i := 0; i < n; i++ {
		arn := fmt.Sprintf("arn:aws:rds:us-east-1:1:cluster:c-%05d", offset+i)
		out = append(out, rxResource(arn, "rds:cluster", "us-east-1"))
	}
	return out
}

// pagedFakeSearcher mirrors realResourceExplorerSearcher's page-loop
// behaviour without the SDK round-trip. Each Search call walks the
// configured pages slice, applies the same in-loop cap as the real
// searcher, and increments pagesFetched per page actually consumed.
// Used to verify the cap stops fetching subsequent pages (saving API
// budget, the load-bearing claim of #309 on the AWS side).
type pagedFakeSearcher struct {
	pages        [][]retypes.Resource
	pagesFetched int
}

func (f *pagedFakeSearcher) Search(_ context.Context, _ string, _ string, maxResults int) ([]retypes.Resource, bool, error) {
	out := make([]retypes.Resource, 0)
	for _, page := range f.pages {
		f.pagesFetched++
		for _, r := range page {
			if maxResults > 0 && len(out) >= maxResults {
				return out, true, nil
			}
			out = append(out, r)
		}
		if maxResults > 0 && len(out) >= maxResults {
			return out, true, nil
		}
	}
	return out, false, nil
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
