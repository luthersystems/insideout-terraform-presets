package awsdiscover

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdtypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
)

// fakeSDClient is the per-region servicediscovery fake. Inputs are
// recorded for paginator-threading assertions; outputs are pulled from
// per-call maps so different namespaces can return different responses.
type fakeSDClient struct {
	pages []servicediscovery.ListNamespacesOutput
	// nsByID maps namespace.Id → GetNamespaceOutput, letting tests
	// configure HostedZoneId per namespace.
	nsByID map[string]*servicediscovery.GetNamespaceOutput
	// tagsByARN maps namespace.Arn → tag list (mirrors the SDK shape).
	tagsByARN map[string][]sdtypes.Tag
	tagsErr   map[string]error
	getNsErr  map[string]error

	mu        sync.Mutex
	listCalls []servicediscovery.ListNamespacesInput
	tagCalls  []string
	getCalls  []string
	listErr   error
}

func (f *fakeSDClient) ListNamespaces(_ context.Context, in *servicediscovery.ListNamespacesInput, _ ...func(*servicediscovery.Options)) (*servicediscovery.ListNamespacesOutput, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, *in)
	idx := len(f.listCalls) - 1
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if idx >= len(f.pages) {
		return &servicediscovery.ListNamespacesOutput{}, nil
	}
	out := f.pages[idx]
	return &out, nil
}

func (f *fakeSDClient) GetNamespace(_ context.Context, in *servicediscovery.GetNamespaceInput, _ ...func(*servicediscovery.Options)) (*servicediscovery.GetNamespaceOutput, error) {
	id := aws.ToString(in.Id)
	f.mu.Lock()
	f.getCalls = append(f.getCalls, id)
	f.mu.Unlock()
	if err, ok := f.getNsErr[id]; ok {
		return nil, err
	}
	if out, ok := f.nsByID[id]; ok {
		return out, nil
	}
	return nil, &sdtypes.NamespaceNotFound{}
}

func (f *fakeSDClient) ListTagsForResource(_ context.Context, in *servicediscovery.ListTagsForResourceInput, _ ...func(*servicediscovery.Options)) (*servicediscovery.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceARN)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErr[arn]; ok {
		return nil, err
	}
	return &servicediscovery.ListTagsForResourceOutput{Tags: f.tagsByARN[arn]}, nil
}

// fakeR53Client is the Route53 fake. VPC associations are keyed by
// hosted zone id.
type fakeR53Client struct {
	vpcsByZone map[string][]r53types.VPC
	errByZone  map[string]error

	mu    sync.Mutex
	calls []string
}

func (f *fakeR53Client) GetHostedZone(_ context.Context, in *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	id := aws.ToString(in.Id)
	f.mu.Lock()
	f.calls = append(f.calls, id)
	f.mu.Unlock()
	if err, ok := f.errByZone[id]; ok {
		return nil, err
	}
	return &route53.GetHostedZoneOutput{
		HostedZone: &r53types.HostedZone{Id: aws.String(id)},
		VPCs:       f.vpcsByZone[id],
	}, nil
}

// sdNamespaceSummary returns a NamespaceSummary populated to mirror
// what ListNamespaces emits for a private DNS namespace.
func sdNamespaceSummary(id, arn, name string) sdtypes.NamespaceSummary {
	return sdtypes.NamespaceSummary{
		Id:   aws.String(id),
		Arn:  aws.String(arn),
		Name: aws.String(name),
		Type: sdtypes.NamespaceTypeDnsPrivate,
	}
}

func sdNamespaceWithHostedZone(id, arn, name, hostedZoneID string) *servicediscovery.GetNamespaceOutput {
	return &servicediscovery.GetNamespaceOutput{
		Namespace: &sdtypes.Namespace{
			Id:   aws.String(id),
			Arn:  aws.String(arn),
			Name: aws.String(name),
			Type: sdtypes.NamespaceTypeDnsPrivate,
			Properties: &sdtypes.NamespaceProperties{
				DnsProperties: &sdtypes.DnsProperties{HostedZoneId: aws.String(hostedZoneID)},
			},
		},
	}
}

func sdTag(k, v string) sdtypes.Tag {
	return sdtypes.Tag{Key: aws.String(k), Value: aws.String(v)}
}

func r53VPC(id string) r53types.VPC {
	return r53types.VPC{VPCId: aws.String(id)}
}

// TestSDPrivateDNSNSDiscover_HappyPath pins the canonical flow: the
// ListNamespaces filter is set to TYPE=DNS_PRIVATE, prefix filter
// gates downstream GetNamespace/ListTagsForResource/GetHostedZone
// calls to project-prefixed names, and the emitted ImportedResource's
// import id is "<namespace_id>:<vpc_id>" with VPC sourced from
// Route53.
func TestSDPrivateDNSNSDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		pages: []servicediscovery.ListNamespacesOutput{{
			Namespaces: []sdtypes.NamespaceSummary{
				sdNamespaceSummary("ns-aaa", "arn:aws:servicediscovery:us-east-1:123:namespace/ns-aaa", "io-foo-internal"),
				sdNamespaceSummary("ns-bbb", "arn:aws:servicediscovery:us-east-1:123:namespace/ns-bbb", "other-foo"),
				sdNamespaceSummary("ns-ccc", "arn:aws:servicediscovery:us-east-1:123:namespace/ns-ccc", "io-foo-svc"),
			},
		}},
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-aaa": sdNamespaceWithHostedZone("ns-aaa", "arn:aws:servicediscovery:us-east-1:123:namespace/ns-aaa", "io-foo-internal", "Z111"),
			"ns-ccc": sdNamespaceWithHostedZone("ns-ccc", "arn:aws:servicediscovery:us-east-1:123:namespace/ns-ccc", "io-foo-svc", "Z333"),
		},
		tagsByARN: map[string][]sdtypes.Tag{
			"arn:aws:servicediscovery:us-east-1:123:namespace/ns-aaa": {sdTag("Project", "io-foo")},
			"arn:aws:servicediscovery:us-east-1:123:namespace/ns-ccc": {sdTag("Project", "io-foo")},
		},
	}
	r53 := &fakeR53Client{
		vpcsByZone: map[string][]r53types.VPC{
			"Z111": {r53VPC("vpc-aaa")},
			"Z333": {r53VPC("vpc-zzz"), r53VPC("vpc-aaa")},
		},
	}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
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
		t.Fatalf("len=%d, want 2 (prefix filter drops other-foo)", len(got))
	}
	// ListNamespaces must carry the TYPE=DNS_PRIVATE filter.
	if len(sd.listCalls) == 0 {
		t.Fatal("no ListNamespaces calls")
	}
	if len(sd.listCalls[0].Filters) != 1 ||
		sd.listCalls[0].Filters[0].Name != sdtypes.NamespaceFilterNameType ||
		len(sd.listCalls[0].Filters[0].Values) != 1 ||
		sd.listCalls[0].Filters[0].Values[0] != string(sdtypes.NamespaceTypeDnsPrivate) {
		t.Errorf("ListNamespaces.Filters=%v, want TYPE=DNS_PRIVATE", sd.listCalls[0].Filters)
	}
	// Prefix filter must gate GetNamespace + ListTagsForResource to
	// the 2 matching rows (not 3).
	if len(sd.getCalls) != 2 {
		t.Errorf("GetNamespace calls=%d, want 2 (prefix-gated)", len(sd.getCalls))
	}
	if len(sd.tagCalls) != 2 {
		t.Errorf("ListTagsForResource calls=%d, want 2 (prefix-gated)", len(sd.tagCalls))
	}
	for _, ir := range got {
		if !strings.Contains(ir.Identity.ImportID, ":") {
			t.Errorf("ImportID=%q missing colon separator", ir.Identity.ImportID)
		}
		parts := strings.Split(ir.Identity.ImportID, ":")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			t.Errorf("ImportID=%q malformed", ir.Identity.ImportID)
		}
		if ir.Identity.NativeIDs["namespace_id"] != parts[0] {
			t.Errorf("namespace_id=%q, want %q (left half of import id)", ir.Identity.NativeIDs["namespace_id"], parts[0])
		}
		if ir.Identity.NativeIDs["vpc_id"] != parts[1] {
			t.Errorf("vpc_id=%q, want %q (right half of import id)", ir.Identity.NativeIDs["vpc_id"], parts[1])
		}
		if ir.Identity.NativeIDs["hosted_zone_id"] == "" {
			t.Errorf("hosted_zone_id missing on emit for %s", ir.Identity.NameHint)
		}
	}
	// Multi-VPC zone Z333 should pick lexicographically-first VPC (vpc-aaa)
	// so re-discovery is stable regardless of API ordering.
	for _, ir := range got {
		if ir.Identity.NativeIDs["namespace_id"] == "ns-ccc" {
			if ir.Identity.NativeIDs["vpc_id"] != "vpc-aaa" {
				t.Errorf("ns-ccc: vpc_id=%q, want vpc-aaa (sorted-first across {vpc-zzz, vpc-aaa})", ir.Identity.NativeIDs["vpc_id"])
			}
		}
	}
}

// TestSDPrivateDNSNSDiscover_PaginatesUntilNoToken pins the
// ListNamespaces paginator: every page's NextToken threads into the
// next request, and the loop terminates only when NextToken is nil
// or "".
func TestSDPrivateDNSNSDiscover_PaginatesUntilNoToken(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		pages: []servicediscovery.ListNamespacesOutput{
			{
				Namespaces: []sdtypes.NamespaceSummary{sdNamespaceSummary("ns-a", "arn:1", "io-foo-a")},
				NextToken:  aws.String("tok1"),
			},
			{
				Namespaces: []sdtypes.NamespaceSummary{sdNamespaceSummary("ns-b", "arn:2", "io-foo-b")},
				NextToken:  aws.String("tok2"),
			},
			{Namespaces: []sdtypes.NamespaceSummary{sdNamespaceSummary("ns-c", "arn:3", "io-foo-c")}}, // terminal
		},
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-a": sdNamespaceWithHostedZone("ns-a", "arn:1", "io-foo-a", "Z1"),
			"ns-b": sdNamespaceWithHostedZone("ns-b", "arn:2", "io-foo-b", "Z2"),
			"ns-c": sdNamespaceWithHostedZone("ns-c", "arn:3", "io-foo-c", "Z3"),
		},
		tagsByARN: map[string][]sdtypes.Tag{
			"arn:1": {sdTag("Project", "io-foo")},
			"arn:2": {sdTag("Project", "io-foo")},
			"arn:3": {sdTag("Project", "io-foo")},
		},
	}
	r53 := &fakeR53Client{vpcsByZone: map[string][]r53types.VPC{
		"Z1": {r53VPC("vpc-1")},
		"Z2": {r53VPC("vpc-2")},
		"Z3": {r53VPC("vpc-3")},
	}}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (paginated)", len(got))
	}
	if len(sd.listCalls) < 3 {
		t.Fatalf("expected at least 3 ListNamespaces calls; got %d", len(sd.listCalls))
	}
	// First call MUST start with nil NextToken — a regression that sent
	// a stale token on the first call would refetch the wrong page.
	if sd.listCalls[0].NextToken != nil {
		t.Errorf("call[0].NextToken=%v, want nil (first call must start unpaginated)", aws.ToString(sd.listCalls[0].NextToken))
	}
	if sd.listCalls[1].NextToken == nil || *sd.listCalls[1].NextToken != "tok1" {
		t.Errorf("call[1].NextToken=%v, want tok1", sd.listCalls[1].NextToken)
	}
	if sd.listCalls[2].NextToken == nil || *sd.listCalls[2].NextToken != "tok2" {
		t.Errorf("call[2].NextToken=%v, want tok2", sd.listCalls[2].NextToken)
	}
}

// TestSDPrivateDNSNSDiscover_TagsErrorSkipsNamespace pins the
// fail-open posture on per-namespace tag errors: a throttled
// ListTagsForResource skips that one namespace, leaves the rest
// of the region's results intact.
func TestSDPrivateDNSNSDiscover_TagsErrorSkipsNamespace(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		pages: []servicediscovery.ListNamespacesOutput{{
			Namespaces: []sdtypes.NamespaceSummary{
				sdNamespaceSummary("ns-good", "arn:good", "io-foo-good"),
				sdNamespaceSummary("ns-bad", "arn:bad", "io-foo-bad"),
			},
		}},
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-good": sdNamespaceWithHostedZone("ns-good", "arn:good", "io-foo-good", "Zgood"),
			"ns-bad":  sdNamespaceWithHostedZone("ns-bad", "arn:bad", "io-foo-bad", "Zbad"),
		},
		tagsByARN: map[string][]sdtypes.Tag{
			"arn:good": {sdTag("Project", "io-foo")},
		},
		tagsErr: map[string]error{
			"arn:bad": errors.New("Throttling"),
		},
	}
	r53 := &fakeR53Client{vpcsByZone: map[string][]r53types.VPC{
		"Zgood": {r53VPC("vpc-good")},
		"Zbad":  {r53VPC("vpc-bad")},
	}}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
	}
	// Capture stderr so we can pin that the tag-fetch failure surfaces
	// as an operator-visible warn (the SUT writes to os.Stderr in the
	// in-errgroup tag-error path; mutation that silently swallows the
	// error would otherwise survive).
	origStderr := os.Stderr
	rPipe, wPipe, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("os.Pipe: %v", pipeErr)
	}
	os.Stderr = wPipe
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	wPipe.Close()
	os.Stderr = origStderr
	stderrBytes, _ := io.ReadAll(rPipe)
	stderr := string(stderrBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Identity.NameHint != "io-foo-good" {
		t.Fatalf("got=%+v, want single io-foo-good", got)
	}
	if !strings.Contains(stderr, "io-foo-bad") || !strings.Contains(stderr, "ListTagsForResource") {
		t.Errorf("stderr=%q, want warn naming the bad namespace + the failing SDK op", stderr)
	}
}

// TestSDPrivateDNSNSDiscover_NoVPCEmitsUnknownAndWarn pins the
// graceful-degradation contract: when Route53 returns no VPCs (or an
// error) for a namespace's hosted zone, the discoverer still emits
// the row with vpc_id=UNKNOWN and surfaces a ServiceWarn so the
// operator sees the unresolvable import.
func TestSDPrivateDNSNSDiscover_NoVPCEmitsUnknownAndWarn(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		pages: []servicediscovery.ListNamespacesOutput{{
			Namespaces: []sdtypes.NamespaceSummary{
				sdNamespaceSummary("ns-1", "arn:1", "io-foo-orphan"),
			},
		}},
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-1": sdNamespaceWithHostedZone("ns-1", "arn:1", "io-foo-orphan", "Z-empty"),
		},
		tagsByARN: map[string][]sdtypes.Tag{
			"arn:1": {sdTag("Project", "io-foo")},
		},
	}
	r53 := &fakeR53Client{
		vpcsByZone: map[string][]r53types.VPC{
			"Z-empty": {}, // private zone with no VPC associations
		},
	}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
	}
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
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (still emit with UNKNOWN VPC)", len(got))
	}
	if got[0].Identity.NativeIDs["vpc_id"] != vpcIDPlaceholderUnknown {
		t.Errorf("vpc_id=%q, want %q", got[0].Identity.NativeIDs["vpc_id"], vpcIDPlaceholderUnknown)
	}
	if !strings.HasSuffix(got[0].Identity.ImportID, ":"+vpcIDPlaceholderUnknown) {
		t.Errorf("ImportID=%q, want suffix :%s", got[0].Identity.ImportID, vpcIDPlaceholderUnknown)
	}
	var warns []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "service_warn" {
			warns = append(warns, e)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("warns=%d, want 1", len(warns))
	}
	if warns[0].Region != "us-east-1" || warns[0].Service != serviceDiscoveryPrivateDNSNamespaceSlug {
		t.Errorf("warn=%+v, want region=us-east-1 service=%s", warns[0], serviceDiscoveryPrivateDNSNamespaceSlug)
	}
	if !strings.Contains(warns[0].Message, "Route53") || !strings.Contains(warns[0].Message, "Z-empty") {
		t.Errorf("warn message=%q, want substrings: Route53, Z-empty", warns[0].Message)
	}
}

// TestSDPrivateDNSNSDiscover_Route53ErrorEmitsUnknown pins the
// Route53-failure branch (distinct from the empty-VPCs branch covered
// above): a GetHostedZone error surfaces as vpc_id=UNKNOWN + ServiceWarn
// rather than aborting the region or silently emitting a wrong import id.
func TestSDPrivateDNSNSDiscover_Route53ErrorEmitsUnknown(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		pages: []servicediscovery.ListNamespacesOutput{{
			Namespaces: []sdtypes.NamespaceSummary{
				sdNamespaceSummary("ns-1", "arn:1", "io-foo-throttled"),
			},
		}},
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-1": sdNamespaceWithHostedZone("ns-1", "arn:1", "io-foo-throttled", "Z-throttle"),
		},
		tagsByARN: map[string][]sdtypes.Tag{
			"arn:1": {sdTag("Project", "io-foo")},
		},
	}
	r53 := &fakeR53Client{errByZone: map[string]error{"Z-throttle": errors.New("Throttling: Route53")}}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
	}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123", Emitter: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (emit with UNKNOWN VPC despite r53 failure)", len(got))
	}
	if got[0].Identity.NativeIDs["vpc_id"] != vpcIDPlaceholderUnknown {
		t.Errorf("vpc_id=%q, want %q", got[0].Identity.NativeIDs["vpc_id"], vpcIDPlaceholderUnknown)
	}
	var warns []recordedEvent
	for _, e := range rec.snapshot() {
		if e.Kind == "service_warn" {
			warns = append(warns, e)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("warns=%d, want 1", len(warns))
	}
	if !strings.Contains(warns[0].Message, "Throttling") {
		t.Errorf("warn=%q, want Route53 error detail preserved", warns[0].Message)
	}
}

// TestSDPrivateDNSNSDiscover_SortsByNamespaceID pins the deterministic
// output ordering: SUT sorts the post-fan-out result by namespace id
// before emitting. errgroup completion order is non-deterministic; a
// regression that drops the sort would surface as flaky CI assertions
// downstream.
func TestSDPrivateDNSNSDiscover_SortsByNamespaceID(t *testing.T) {
	t.Parallel()
	shuffled := []sdtypes.NamespaceSummary{
		sdNamespaceSummary("ns-zzz", "arn:zzz", "io-foo-zzz"),
		sdNamespaceSummary("ns-aaa", "arn:aaa", "io-foo-aaa"),
		sdNamespaceSummary("ns-mmm", "arn:mmm", "io-foo-mmm"),
	}
	sd := &fakeSDClient{
		pages: []servicediscovery.ListNamespacesOutput{{Namespaces: shuffled}},
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-zzz": sdNamespaceWithHostedZone("ns-zzz", "arn:zzz", "io-foo-zzz", "Zzzz"),
			"ns-aaa": sdNamespaceWithHostedZone("ns-aaa", "arn:aaa", "io-foo-aaa", "Zaaa"),
			"ns-mmm": sdNamespaceWithHostedZone("ns-mmm", "arn:mmm", "io-foo-mmm", "Zmmm"),
		},
		tagsByARN: map[string][]sdtypes.Tag{
			"arn:zzz": {sdTag("Project", "io-foo")},
			"arn:aaa": {sdTag("Project", "io-foo")},
			"arn:mmm": {sdTag("Project", "io-foo")},
		},
	}
	r53 := &fakeR53Client{vpcsByZone: map[string][]r53types.VPC{
		"Zzzz": {r53VPC("vpc-z")},
		"Zaaa": {r53VPC("vpc-a")},
		"Zmmm": {r53VPC("vpc-m")},
	}}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	wantOrder := []string{"io-foo-aaa", "io-foo-mmm", "io-foo-zzz"}
	for i, want := range wantOrder {
		if got[i].Identity.NameHint != want {
			t.Errorf("position %d: NameHint=%q, want %q (SUT must sort by namespace_id before emit)", i, got[i].Identity.NameHint, want)
		}
	}
}

// TestSDPrivateDNSNSDiscover_ListErrorAbortsRegion pins region-fatal
// failure semantics: a ListNamespaces error surfaces as the outer
// error (not a per-item warn), preserving the bedrock_guardrail /
// apigatewayv2_stage shape.
func TestSDPrivateDNSNSDiscover_ListErrorAbortsRegion(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: servicediscovery:ListNamespaces")
	sd := &fakeSDClient{listErr: sentinel}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return &fakeR53Client{} },
		maxConcurrency: 4,
	}
	rec := &recordingEmitter{}
	_, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123", Emitter: rec,
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
	// ServiceFinish must still close the bracket on the abort path so
	// timing telemetry isn't corrupted (cloudControlDiscoverer
	// precedent).
	var starts, finishes int
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			starts++
		case "service_finish":
			finishes++
		}
	}
	if starts != 1 || finishes != 1 {
		t.Errorf("abort path: starts=%d finishes=%d, want 1 each", starts, finishes)
	}
}

func TestSDPrivateDNSNSDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	sdByRegion := map[string]*fakeSDClient{
		"us-east-1": {
			pages: []servicediscovery.ListNamespacesOutput{{
				Namespaces: []sdtypes.NamespaceSummary{sdNamespaceSummary("ns-east", "arn:east", "io-foo-east")},
			}},
			nsByID: map[string]*servicediscovery.GetNamespaceOutput{
				"ns-east": sdNamespaceWithHostedZone("ns-east", "arn:east", "io-foo-east", "Zeast"),
			},
			tagsByARN: map[string][]sdtypes.Tag{"arn:east": {sdTag("Project", "io-foo")}},
		},
		"eu-west-1": {
			pages: []servicediscovery.ListNamespacesOutput{{
				Namespaces: []sdtypes.NamespaceSummary{sdNamespaceSummary("ns-west", "arn:west", "io-foo-west")},
			}},
			nsByID: map[string]*servicediscovery.GetNamespaceOutput{
				"ns-west": sdNamespaceWithHostedZone("ns-west", "arn:west", "io-foo-west", "Zwest"),
			},
			tagsByARN: map[string][]sdtypes.Tag{"arn:west": {sdTag("Project", "io-foo")}},
		},
	}
	r53 := &fakeR53Client{vpcsByZone: map[string][]r53types.VPC{
		"Zeast": {r53VPC("vpc-east")},
		"Zwest": {r53VPC("vpc-west")},
	}}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(region string) serviceDiscoveryPrivateDNSNamespaceClient { return sdByRegion[region] },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
	}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1", "eu-west-1"},
		AccountID: "123",
		Emitter:   rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != serviceDiscoveryPrivateDNSNamespaceSlug {
				t.Errorf("service_start.service=%q, want %s", e.Service, serviceDiscoveryPrivateDNSNamespaceSlug)
			}
			starts[e.Region]++
		case "service_finish":
			finishes[e.Region]++
		}
	}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if starts[region] != 1 {
			t.Errorf("region=%s: service_start count=%d, want 1", region, starts[region])
		}
		if finishes[region] != 1 {
			t.Errorf("region=%s: service_finish count=%d, want 1", region, finishes[region])
		}
	}
}

// TestSDPrivateDNSNSDiscover_EmptyProjectReturnsAll mirrors the
// bedrock_guardrail empty-Project contract: no prefix filter means
// every namespace surfaces, and tag fetches fan out across all of
// them.
func TestSDPrivateDNSNSDiscover_EmptyProjectReturnsAll(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		pages: []servicediscovery.ListNamespacesOutput{{
			Namespaces: []sdtypes.NamespaceSummary{
				sdNamespaceSummary("ns-1", "arn:1", "alpha"),
				sdNamespaceSummary("ns-2", "arn:2", "beta"),
			},
		}},
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-1": sdNamespaceWithHostedZone("ns-1", "arn:1", "alpha", "Z1"),
			"ns-2": sdNamespaceWithHostedZone("ns-2", "arn:2", "beta", "Z2"),
		},
		tagsByARN: map[string][]sdtypes.Tag{
			"arn:1": {},
			"arn:2": {},
		},
	}
	r53 := &fakeR53Client{vpcsByZone: map[string][]r53types.VPC{
		"Z1": {r53VPC("vpc-1")},
		"Z2": {r53VPC("vpc-2")},
	}}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:          func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53:         func() route53HostedZoneClient { return r53 },
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (no prefix filter)", len(got))
	}
	if len(sd.tagCalls) != 2 {
		t.Errorf("ListTagsForResource calls=%d, want 2 (no prefix gate)", len(sd.tagCalls))
	}
}

func TestSDPrivateDNSNSDiscoverByID_AcceptsImportID(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-1": sdNamespaceWithHostedZone("ns-1", "arn:1", "io-foo-svc", "Z1"),
		},
	}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:  func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53: func() route53HostedZoneClient { return &fakeR53Client{} },
	}
	got, err := d.DiscoverByID(context.Background(), "ns-1:vpc-123", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.ImportID != "ns-1:vpc-123" {
		t.Errorf("ImportID=%q, want ns-1:vpc-123 (caller-supplied vpc wins)", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["namespace_id"] != "ns-1" {
		t.Errorf("namespace_id=%q", got.Identity.NativeIDs["namespace_id"])
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-123" {
		t.Errorf("vpc_id=%q", got.Identity.NativeIDs["vpc_id"])
	}
}

func TestSDPrivateDNSNSDiscoverByID_BareIDResolvesVPCViaRoute53(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-1": sdNamespaceWithHostedZone("ns-1", "arn:1", "io-foo-svc", "Z1"),
		},
	}
	r53 := &fakeR53Client{vpcsByZone: map[string][]r53types.VPC{"Z1": {r53VPC("vpc-resolved")}}}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:  func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53: func() route53HostedZoneClient { return r53 },
	}
	got, err := d.DiscoverByID(context.Background(), "ns-1", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.NativeIDs["vpc_id"] != "vpc-resolved" {
		t.Errorf("vpc_id=%q, want vpc-resolved (route53 hop on bare-id)", got.Identity.NativeIDs["vpc_id"])
	}
	if got.Identity.ImportID != "ns-1:vpc-resolved" {
		t.Errorf("ImportID=%q, want ns-1:vpc-resolved", got.Identity.ImportID)
	}
}

func TestSDPrivateDNSNSDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:  func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return &fakeSDClient{} },
		newR53: func() route53HostedZoneClient { return &fakeR53Client{} },
	}
	// Cover both shapes the caller can submit:
	//   bare namespace-id (no colon) — triggers post-fetch r53 hop
	//   "<ns>:<vpc>" — caller supplied vpc, no r53 hop
	// Both must surface NamespaceNotFound as ErrNotFound so dep-chase
	// can recognize the "resource gone" condition uniformly.
	for _, id := range []string{"ns-missing", "ns-missing:vpc-stale"} {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("id=%q: err=%v, want ErrNotFound", id, err)
		}
	}
}

func TestSDPrivateDNSNSDiscoverByID_RejectsWrongNamespaceType(t *testing.T) {
	t.Parallel()
	sd := &fakeSDClient{
		nsByID: map[string]*servicediscovery.GetNamespaceOutput{
			"ns-pub": {
				Namespace: &sdtypes.Namespace{
					Id:   aws.String("ns-pub"),
					Type: sdtypes.NamespaceTypeDnsPublic,
				},
			},
		},
	}
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:  func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return sd },
		newR53: func() route53HostedZoneClient { return &fakeR53Client{} },
	}
	_, err := d.DiscoverByID(context.Background(), "ns-pub:vpc-1", "us-east-1", "123")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("err=%v, want ErrNotSupported (wrong namespace type)", err)
	}
}

func TestSDPrivateDNSNSDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD:  func(_ string) serviceDiscoveryPrivateDNSNamespaceClient { return &fakeSDClient{} },
		newR53: func() route53HostedZoneClient { return &fakeR53Client{} },
	}
	cases := []string{
		"",
		"a:b:c", // too many colons
		"a/b",   // slash
		"ns:",   // empty vpc half
		":vpc",  // empty ns half
		"ns vpc",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}
