package awsdiscover

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// errLBListenerSeed pins the SDK-error propagation contract for the
// listener discoverer.
var errLBListenerSeed = errors.New("AccessDenied")

type fakeLBListenerClient struct {
	lbPages          []elasticloadbalancingv2.DescribeLoadBalancersOutput
	lbErr            error
	listenersByLBArn map[string][]elbv2types.Listener
	listenersErr     error
	listenerByArn    map[string]elbv2types.Listener
	tagsByArn        map[string][]elbv2types.Tag
	tagsErr          map[string]error

	mu              sync.Mutex
	lbCalls         []elasticloadbalancingv2.DescribeLoadBalancersInput
	listenerCalls   []elasticloadbalancingv2.DescribeListenersInput
	listenerByLBArn []string
	tagCalls        [][]string
}

func (f *fakeLBListenerClient) DescribeLoadBalancers(_ context.Context, in *elasticloadbalancingv2.DescribeLoadBalancersInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error) {
	f.mu.Lock()
	f.lbCalls = append(f.lbCalls, *in)
	idx := len(f.lbCalls) - 1
	f.mu.Unlock()
	if f.lbErr != nil {
		return nil, f.lbErr
	}
	if idx >= len(f.lbPages) {
		return &elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil
	}
	return &f.lbPages[idx], nil
}

func (f *fakeLBListenerClient) DescribeListeners(_ context.Context, in *elasticloadbalancingv2.DescribeListenersInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeListenersOutput, error) {
	f.mu.Lock()
	f.listenerCalls = append(f.listenerCalls, *in)
	if in.LoadBalancerArn != nil {
		f.listenerByLBArn = append(f.listenerByLBArn, aws.ToString(in.LoadBalancerArn))
	}
	f.mu.Unlock()
	if f.listenersErr != nil {
		return nil, f.listenersErr
	}
	if len(in.ListenerArns) == 1 {
		// DiscoverByID single-listener lookup.
		if l, ok := f.listenerByArn[in.ListenerArns[0]]; ok {
			return &elasticloadbalancingv2.DescribeListenersOutput{Listeners: []elbv2types.Listener{l}}, nil
		}
		return nil, &elbv2types.ListenerNotFoundException{}
	}
	if in.LoadBalancerArn == nil {
		return &elasticloadbalancingv2.DescribeListenersOutput{}, nil
	}
	listeners := f.listenersByLBArn[aws.ToString(in.LoadBalancerArn)]
	return &elasticloadbalancingv2.DescribeListenersOutput{Listeners: listeners}, nil
}

func (f *fakeLBListenerClient) DescribeTags(_ context.Context, in *elasticloadbalancingv2.DescribeTagsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error) {
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, append([]string(nil), in.ResourceArns...))
	f.mu.Unlock()
	out := &elasticloadbalancingv2.DescribeTagsOutput{}
	for _, arn := range in.ResourceArns {
		if err, ok := f.tagsErr[arn]; ok {
			return nil, err
		}
		out.TagDescriptions = append(out.TagDescriptions, elbv2types.TagDescription{
			ResourceArn: aws.String(arn),
			Tags:        f.tagsByArn[arn],
		})
	}
	return out, nil
}

func listenerFixture(arn, lbArn string, port int32, protocol elbv2types.ProtocolEnum) elbv2types.Listener {
	return elbv2types.Listener{
		ListenerArn:     aws.String(arn),
		LoadBalancerArn: aws.String(lbArn),
		Port:            aws.Int32(port),
		Protocol:        protocol,
	}
}

func TestLBListenerDiscover_HappyPath(t *testing.T) {
	t.Parallel()
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc"
	lb := lbFixture("io-foo-app", lbArn, "io-foo-app.elb", "vpc-1", elbv2types.LoadBalancerTypeEnumApplication)
	ln1 := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-app/abc/ln-80", lbArn, 80, elbv2types.ProtocolEnumHttp)
	ln2 := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-app/abc/ln-443", lbArn, 443, elbv2types.ProtocolEnumHttps)
	d := &lbListenerDiscoverer{
		new: func(_ string) lbListenerClient {
			return &fakeLBListenerClient{
				lbPages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb}}},
				listenersByLBArn: map[string][]elbv2types.Listener{
					lbArn: {ln1, ln2},
				},
				tagsByArn: map[string][]elbv2types.Tag{
					aws.ToString(ln1.ListenerArn): {elbv2Tag("Project", "io-foo")},
					aws.ToString(ln2.ListenerArn): {elbv2Tag("Project", "io-foo")},
				},
			}
		},
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	for _, ir := range got {
		if ir.Identity.Type != lbListenerTFType {
			t.Errorf("Type=%q", ir.Identity.Type)
		}
		for _, k := range []string{"listener_arn", "lb_arn", "protocol", "port"} {
			if ir.Identity.NativeIDs[k] == "" {
				t.Errorf("NativeIDs[%s] empty for %q", k, ir.Identity.NameHint)
			}
		}
	}
	// Listener NameHint is "<lb-name>-<port>". Ports 80,443 → names sorted by ARN.
	wantNames := map[string]bool{"io-foo-app-80": true, "io-foo-app-443": true}
	for _, ir := range got {
		if !wantNames[ir.Identity.NameHint] {
			t.Errorf("NameHint=%q not in expected set %v", ir.Identity.NameHint, wantNames)
		}
	}
}

func TestLBListenerDiscover_PaginatesUntilNoMarker(t *testing.T) {
	t.Parallel()
	// Pagination here is on the parent DescribeLoadBalancers call. Two
	// LBs spread across two pages, each with one listener — final count
	// should be 2 listeners.
	lb1Arn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-a/aaa"
	lb2Arn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-b/bbb"
	lb1 := lbFixture("io-foo-a", lb1Arn, "a.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("io-foo-b", lb2Arn, "b.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	ln1 := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-a/aaa/ln-1", lb1Arn, 80, elbv2types.ProtocolEnumHttp)
	ln2 := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-b/bbb/ln-2", lb2Arn, 80, elbv2types.ProtocolEnumHttp)
	d := &lbListenerDiscoverer{
		new: func(_ string) lbListenerClient {
			return &fakeLBListenerClient{
				lbPages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{
					{LoadBalancers: []elbv2types.LoadBalancer{lb1}, NextMarker: aws.String("p2")},
					{LoadBalancers: []elbv2types.LoadBalancer{lb2}}, // terminal
				},
				listenersByLBArn: map[string][]elbv2types.Listener{
					lb1Arn: {ln1},
					lb2Arn: {ln2},
				},
				tagsByArn: map[string][]elbv2types.Tag{
					aws.ToString(ln1.ListenerArn): {elbv2Tag("Project", "io-foo")},
					aws.ToString(ln2.ListenerArn): {elbv2Tag("Project", "io-foo")},
				},
			}
		},
		maxConcurrency: 4,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (paginated)", len(got))
	}
}

func TestLBListenerDiscover_FiltersByProjectPrefix(t *testing.T) {
	t.Parallel()
	keptLBArn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/aaa"
	skipLBArn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/other-team/bbb"
	keptLB := lbFixture("io-foo-app", keptLBArn, "k.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	skipLB := lbFixture("other-team", skipLBArn, "s.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	keptLn := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-app/aaa/ln-80", keptLBArn, 80, elbv2types.ProtocolEnumHttp)
	skipLn := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/other-team/bbb/ln-80", skipLBArn, 80, elbv2types.ProtocolEnumHttp)
	fake := &fakeLBListenerClient{
		lbPages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{keptLB, skipLB}}},
		listenersByLBArn: map[string][]elbv2types.Listener{
			keptLBArn: {keptLn},
			skipLBArn: {skipLn},
		},
		tagsByArn: map[string][]elbv2types.Tag{
			aws.ToString(keptLn.ListenerArn): {elbv2Tag("Project", "io-foo")},
		},
	}
	d := &lbListenerDiscoverer{new: func(_ string) lbListenerClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (only listeners on prefix-matching LB)", len(got))
	}
	if got[0].Identity.NativeIDs["lb_arn"] != keptLBArn {
		t.Errorf("emitted listener bound to wrong LB: %q", got[0].Identity.NativeIDs["lb_arn"])
	}
	// Pin: prefix gates DescribeListeners — non-matching LB must not have
	// produced a listener-listing call.
	for _, lbArn := range fake.listenerByLBArn {
		if lbArn == skipLBArn {
			t.Errorf("DescribeListeners called for non-prefix-matching LB %q (%v)", skipLBArn, fake.listenerByLBArn)
		}
	}
}

func TestLBListenerDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	d := &lbListenerDiscoverer{
		new: func(_ string) lbListenerClient {
			return &fakeLBListenerClient{lbErr: errLBListenerSeed}
		},
		maxConcurrency: 4,
	}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errLBListenerSeed) {
		t.Errorf("err=%v, want errors.Is(err, errLBListenerSeed)", err)
	}
}

func TestLBListenerDiscoverByID_AcceptsID(t *testing.T) {
	t.Parallel()
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc"
	lnArn := "arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-app/abc/ln-80"
	ln := listenerFixture(lnArn, lbArn, 80, elbv2types.ProtocolEnumHttp)
	d := &lbListenerDiscoverer{
		new: func(_ string) lbListenerClient {
			return &fakeLBListenerClient{listenerByArn: map[string]elbv2types.Listener{lnArn: ln}}
		},
	}
	got, err := d.DiscoverByID(context.Background(), lnArn, "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != lbListenerTFType {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != lnArn {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, lnArn)
	}
	if got.Identity.NativeIDs["lb_arn"] != lbArn {
		t.Errorf("NativeIDs[lb_arn]=%q", got.Identity.NativeIDs["lb_arn"])
	}
	if got.Identity.NativeIDs["port"] != "80" {
		t.Errorf("NativeIDs[port]=%q", got.Identity.NativeIDs["port"])
	}
	if got.Identity.NameHint != "io-foo-app-80" {
		t.Errorf("NameHint=%q, want io-foo-app-80", got.Identity.NameHint)
	}
}

func TestLBListenerDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	d := &lbListenerDiscoverer{new: func(_ string) lbListenerClient { return &fakeLBListenerClient{} }}
	_, err := d.DiscoverByID(context.Background(), "arn:aws:elasticloadbalancing:us-east-1:123:listener/app/foo/bar/missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestLBListenerDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	mkClient := func(region string) *fakeLBListenerClient {
		lbArn := "arn:aws:elasticloadbalancing:" + region + ":123:loadbalancer/app/io-foo/aaa"
		lnArn := "arn:aws:elasticloadbalancing:" + region + ":123:listener/app/io-foo/aaa/ln-80"
		lb := lbFixture("io-foo", lbArn, region+".elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
		ln := listenerFixture(lnArn, lbArn, 80, elbv2types.ProtocolEnumHttp)
		return &fakeLBListenerClient{
			lbPages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb}}},
			listenersByLBArn: map[string][]elbv2types.Listener{
				lbArn: {ln},
			},
			tagsByArn: map[string][]elbv2types.Tag{
				lnArn: {elbv2Tag("Project", "io-foo")},
			},
		}
	}
	clients := map[string]*fakeLBListenerClient{
		"us-east-1": mkClient("us-east-1"),
		"eu-west-1": mkClient("eu-west-1"),
	}
	d := &lbListenerDiscoverer{new: func(region string) lbListenerClient { return clients[region] }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one listener per region)", len(got))
	}
	for region, fake := range clients {
		if len(fake.lbCalls) < 1 {
			t.Errorf("region=%s: expected ≥1 DescribeLoadBalancers call; got %d", region, len(fake.lbCalls))
		}
	}
}

func TestLBListenerDiscover_TagSelectorAppliedAsFilter(t *testing.T) {
	t.Parallel()
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-app/abc"
	lb := lbFixture("io-foo-app", lbArn, "k.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	prodArn := "arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-app/abc/ln-80"
	stagArn := "arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-app/abc/ln-443"
	prodLn := listenerFixture(prodArn, lbArn, 80, elbv2types.ProtocolEnumHttp)
	stagLn := listenerFixture(stagArn, lbArn, 443, elbv2types.ProtocolEnumHttps)
	d := &lbListenerDiscoverer{
		new: func(_ string) lbListenerClient {
			return &fakeLBListenerClient{
				lbPages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb}}},
				listenersByLBArn: map[string][]elbv2types.Listener{
					lbArn: {prodLn, stagLn},
				},
				tagsByArn: map[string][]elbv2types.Tag{
					prodArn: {elbv2Tag("env", "prod")},
					stagArn: {elbv2Tag("env", "staging")},
				},
			}
		},
		maxConcurrency: 4,
	}
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
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Identity.Tags["env"] != "prod" {
		t.Errorf("Tags[env]=%q", got[0].Identity.Tags["env"])
	}
}

func TestLBListenerDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	mkClient := func(region string) *fakeLBListenerClient {
		lbArn := "arn:aws:elasticloadbalancing:" + region + ":123:loadbalancer/app/io-foo/aaa"
		lnArn := "arn:aws:elasticloadbalancing:" + region + ":123:listener/app/io-foo/aaa/ln-80"
		lb := lbFixture("io-foo", lbArn, "x", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
		ln := listenerFixture(lnArn, lbArn, 80, elbv2types.ProtocolEnumHttp)
		return &fakeLBListenerClient{
			lbPages:          []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb}}},
			listenersByLBArn: map[string][]elbv2types.Listener{lbArn: {ln}},
			tagsByArn:        map[string][]elbv2types.Tag{lnArn: {elbv2Tag("Project", "io-foo")}},
		}
	}
	clients := map[string]*fakeLBListenerClient{"us-east-1": mkClient("us-east-1"), "eu-west-1": mkClient("eu-west-1")}
	d := &lbListenerDiscoverer{new: func(region string) lbListenerClient { return clients[region] }, maxConcurrency: 4}
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
			if e.Service != lbListenerSlug {
				t.Errorf("service_start.service=%q", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != lbListenerSlug {
				t.Errorf("service_finish.service=%q", e.Service)
			}
			finishes[e.Region]++
		}
	}
	for _, r := range []string{"us-east-1", "eu-west-1"} {
		if starts[r] != 1 || finishes[r] != 1 {
			t.Errorf("region=%s: start=%d finish=%d, want 1/1", r, starts[r], finishes[r])
		}
	}
}

func TestLBListenerDiscover_EmitsItemFound_PerResource(t *testing.T) {
	t.Parallel()
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo/aaa"
	lb := lbFixture("io-foo", lbArn, "x", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	listeners := []elbv2types.Listener{
		listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo/aaa/ln-80", lbArn, 80, elbv2types.ProtocolEnumHttp),
		listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo/aaa/ln-81", lbArn, 81, elbv2types.ProtocolEnumHttp),
		listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo/aaa/ln-443", lbArn, 443, elbv2types.ProtocolEnumHttps),
	}
	tagsByArn := map[string][]elbv2types.Tag{}
	for _, ln := range listeners {
		tagsByArn[aws.ToString(ln.ListenerArn)] = []elbv2types.Tag{elbv2Tag("Project", "io-foo")}
	}
	d := &lbListenerDiscoverer{
		new: func(_ string) lbListenerClient {
			return &fakeLBListenerClient{
				lbPages:          []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb}}},
				listenersByLBArn: map[string][]elbv2types.Listener{lbArn: listeners},
				tagsByArn:        tagsByArn,
			}
		},
		maxConcurrency: 4,
	}
	rec := &recordingEmitter{}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123", Emitter: rec})
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
	for _, it := range items {
		if it.Service != lbListenerSlug {
			t.Errorf("item.service=%q", it.Service)
		}
		if it.TFType != lbListenerTFType {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
	}
}

func TestLBListenerDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &lbListenerDiscoverer{new: func(_ string) lbListenerClient { return &fakeLBListenerClient{} }}
	cases := []string{
		"",
		"some-bare-name",        // listeners require ARN form
		"arn:aws:s3:::a-bucket", // wrong service
		"arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/foo/bar", // wrong shape (loadbalancer, not listener)
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestLBListenerDiscover_IteratesPerLoadBalancer is the per-type extra:
// pin the multi-step listing contract — DescribeListeners must be called
// exactly once per prefix-matching LB (not once total, and not for
// non-matching LBs). A regression that flattened the per-LB iteration
// would produce zero or one DescribeListeners calls instead of N.
func TestLBListenerDiscover_IteratesPerLoadBalancer(t *testing.T) {
	t.Parallel()
	lb1Arn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-a/aaa"
	lb2Arn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/io-foo-b/bbb"
	skipArn := "arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/skip-me/ccc"
	lb1 := lbFixture("io-foo-a", lb1Arn, "a.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	lb2 := lbFixture("io-foo-b", lb2Arn, "b.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	skip := lbFixture("skip-me", skipArn, "s.elb", "vpc", elbv2types.LoadBalancerTypeEnumApplication)
	ln1 := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-a/aaa/ln-80", lb1Arn, 80, elbv2types.ProtocolEnumHttp)
	ln2 := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/io-foo-b/bbb/ln-80", lb2Arn, 80, elbv2types.ProtocolEnumHttp)
	skipLn := listenerFixture("arn:aws:elasticloadbalancing:us-east-1:123:listener/app/skip-me/ccc/ln-80", skipArn, 80, elbv2types.ProtocolEnumHttp)
	fake := &fakeLBListenerClient{
		lbPages: []elasticloadbalancingv2.DescribeLoadBalancersOutput{{LoadBalancers: []elbv2types.LoadBalancer{lb1, lb2, skip}}},
		listenersByLBArn: map[string][]elbv2types.Listener{
			lb1Arn:  {ln1},
			lb2Arn:  {ln2},
			skipArn: {skipLn},
		},
		tagsByArn: map[string][]elbv2types.Tag{
			aws.ToString(ln1.ListenerArn): {elbv2Tag("Project", "io-foo")},
			aws.ToString(ln2.ListenerArn): {elbv2Tag("Project", "io-foo")},
		},
	}
	d := &lbListenerDiscoverer{new: func(_ string) lbListenerClient { return fake }, maxConcurrency: 4}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	// Pin: DescribeListeners called exactly twice (once per prefix-
	// matching LB). The skipped LB's listener must not appear in the
	// listener-by-LB-arn trace.
	calls := map[string]int{}
	for _, lbArn := range fake.listenerByLBArn {
		calls[lbArn]++
	}
	if calls[lb1Arn] != 1 {
		t.Errorf("DescribeListeners(LoadBalancerArn=%q) called %d times, want 1", lb1Arn, calls[lb1Arn])
	}
	if calls[lb2Arn] != 1 {
		t.Errorf("DescribeListeners(LoadBalancerArn=%q) called %d times, want 1", lb2Arn, calls[lb2Arn])
	}
	if calls[skipArn] != 0 {
		t.Errorf("DescribeListeners called %d times for non-matching LB %q (want 0)", calls[skipArn], skipArn)
	}
}
