package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// errEKSPodIdentitySeed is the package-level sentinel for ListClusters
// error-propagation assertions (canonical err<Service>Seed naming).
// errors.Is is the right tool — asserting only that err != nil masks
// regressions where the discover layer silently wraps or replaces the
// underlying SDK error.
var errEKSPodIdentitySeed = errors.New("AccessDenied")

type fakeEKSPodIdentityClient struct {
	clustersPages          []eks.ListClustersOutput
	listClustersErr        error
	assocsByCluster        map[string][]ekstypes.PodIdentityAssociationSummary
	listAssocsErrByCluster map[string]error
	tagsByARN              map[string]map[string]string
	tagsErrByARN           map[string]error

	mu                 sync.Mutex
	listClustersCalls  []eks.ListClustersInput
	listAssocsCalls    []eks.ListPodIdentityAssociationsInput
	tagCalls           []string
	describeCalls      []eks.DescribePodIdentityAssociationInput
	describeByID       map[string]*ekstypes.PodIdentityAssociation
	describeErr        error
	describeReturnsErr bool
}

func (f *fakeEKSPodIdentityClient) ListClusters(_ context.Context, in *eks.ListClustersInput, _ ...func(*eks.Options)) (*eks.ListClustersOutput, error) {
	f.mu.Lock()
	f.listClustersCalls = append(f.listClustersCalls, *in)
	idx := len(f.listClustersCalls) - 1
	f.mu.Unlock()
	if f.listClustersErr != nil {
		return nil, f.listClustersErr
	}
	if idx >= len(f.clustersPages) {
		return &eks.ListClustersOutput{}, nil
	}
	return &f.clustersPages[idx], nil
}

func (f *fakeEKSPodIdentityClient) ListPodIdentityAssociations(_ context.Context, in *eks.ListPodIdentityAssociationsInput, _ ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error) {
	cluster := aws.ToString(in.ClusterName)
	f.mu.Lock()
	f.listAssocsCalls = append(f.listAssocsCalls, *in)
	f.mu.Unlock()
	if err, ok := f.listAssocsErrByCluster[cluster]; ok {
		return nil, err
	}
	return &eks.ListPodIdentityAssociationsOutput{Associations: f.assocsByCluster[cluster]}, nil
}

func (f *fakeEKSPodIdentityClient) DescribePodIdentityAssociation(_ context.Context, in *eks.DescribePodIdentityAssociationInput, _ ...func(*eks.Options)) (*eks.DescribePodIdentityAssociationOutput, error) {
	f.mu.Lock()
	f.describeCalls = append(f.describeCalls, *in)
	f.mu.Unlock()
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	id := aws.ToString(in.AssociationId)
	if a, ok := f.describeByID[id]; ok {
		return &eks.DescribePodIdentityAssociationOutput{Association: a}, nil
	}
	return nil, &ekstypes.ResourceNotFoundException{}
}

func (f *fakeEKSPodIdentityClient) ListTagsForResource(_ context.Context, in *eks.ListTagsForResourceInput, _ ...func(*eks.Options)) (*eks.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
	f.mu.Lock()
	f.tagCalls = append(f.tagCalls, arn)
	f.mu.Unlock()
	if err, ok := f.tagsErrByARN[arn]; ok {
		return nil, err
	}
	return &eks.ListTagsForResourceOutput{Tags: f.tagsByARN[arn]}, nil
}

func assoc(cluster, id, arn, ns, sa string) ekstypes.PodIdentityAssociationSummary {
	return ekstypes.PodIdentityAssociationSummary{
		ClusterName:    aws.String(cluster),
		AssociationId:  aws.String(id),
		AssociationArn: aws.String(arn),
		Namespace:      aws.String(ns),
		ServiceAccount: aws.String(sa),
	}
}

func TestEKSPodIdentityDiscover_FiltersByClusterPrefix(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{
		clustersPages: []eks.ListClustersOutput{
			{Clusters: []string{"io-foo-a", "io-foo-b", "other-c"}},
		},
		assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
			"io-foo-a": {assoc("io-foo-a", "a-1", "arn:aws:eks:us-east-1:123:podidentityassociation/io-foo-a/a-1", "default", "sa-1")},
			"io-foo-b": {assoc("io-foo-b", "b-1", "arn:aws:eks:us-east-1:123:podidentityassociation/io-foo-b/b-1", "default", "sa-2")},
			"other-c":  {assoc("other-c", "c-1", "arn:aws:eks:us-east-1:123:podidentityassociation/other-c/c-1", "default", "sa-3")},
		},
		tagsByARN: map[string]map[string]string{
			"arn:aws:eks:us-east-1:123:podidentityassociation/io-foo-a/a-1": {"Project": "io-foo"},
			"arn:aws:eks:us-east-1:123:podidentityassociation/io-foo-b/b-1": {},
		},
	}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (only io-foo-* clusters)", len(got))
	}
	for _, ir := range got {
		if ir.Identity.NativeIDs["cluster_name"] == "other-c" {
			t.Error("association on non-prefix-matching cluster leaked through filter")
		}
		if ir.Identity.ImportID == "" {
			t.Error("ImportID empty")
		}
		if !contains2Commas(ir.Identity.ImportID) {
			t.Errorf("ImportID=%q does not have <cluster>,<id> shape", ir.Identity.ImportID)
		}
	}
	// Pin: ListPodIdentityAssociations must NOT have been called for
	// the non-matching cluster — that's the whole point of prefix
	// filtering on cluster name.
	for _, c := range fake.listAssocsCalls {
		if aws.ToString(c.ClusterName) == "other-c" {
			t.Errorf("ListPodIdentityAssociations called for non-prefix cluster %q; prefix gating regressed", "other-c")
		}
	}
}

func contains2Commas(s string) bool {
	count := 0
	for _, r := range s {
		if r == ',' {
			count++
		}
	}
	return count == 1
}

// TestEKSPodIdentityDiscover_IteratesPerCluster pins the 2-step list
// shape: one ListPodIdentityAssociations per prefix-matching cluster.
// A regression that drops the inner loop (e.g. only calls it for the
// first cluster) would surface here.
func TestEKSPodIdentityDiscover_IteratesPerCluster(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{
		clustersPages: []eks.ListClustersOutput{
			{Clusters: []string{"io-foo-a", "io-foo-b", "io-foo-c"}},
		},
		assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
			"io-foo-a": {assoc("io-foo-a", "a-1", "arn-a-1", "default", "sa")},
			"io-foo-b": {assoc("io-foo-b", "b-1", "arn-b-1", "default", "sa")},
			"io-foo-c": {}, // empty list — but ListPodIdentityAssociations must still be called
		},
	}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d, want 2 (one per non-empty cluster)", len(got))
	}
	if len(fake.listAssocsCalls) != 3 {
		t.Errorf("ListPodIdentityAssociations called %d time(s); want 3 (one per prefix-matching cluster)", len(fake.listAssocsCalls))
	}
}

func TestEKSPodIdentityDiscover_PaginatesClusters(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{
		clustersPages: []eks.ListClustersOutput{
			{Clusters: []string{"io-foo-a"}, NextToken: aws.String("nt1")},
			{Clusters: []string{"io-foo-b"}}, // terminal
		},
		assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
			"io-foo-a": {assoc("io-foo-a", "a-1", "arn-a-1", "default", "sa")},
			"io-foo-b": {assoc("io-foo-b", "b-1", "arn-b-1", "default", "sa")},
		},
	}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (paginated)", len(got))
	}
	// Pin: the second ListClusters call must have used the NextToken
	// from the first response — drift here means pagination is broken.
	if len(fake.listClustersCalls) != 2 {
		t.Fatalf("ListClusters called %d time(s); want 2", len(fake.listClustersCalls))
	}
	if aws.ToString(fake.listClustersCalls[1].NextToken) != "nt1" {
		t.Errorf("second ListClusters call NextToken=%q, want nt1", aws.ToString(fake.listClustersCalls[1].NextToken))
	}
}

func TestEKSPodIdentityDiscover_PropagatesError(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{listClustersErr: errEKSPodIdentitySeed}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }}
	_, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, errEKSPodIdentitySeed) {
		t.Errorf("err=%v, want errors.Is(err, errEKSPodIdentitySeed)", err)
	}
}

func TestEKSPodIdentityDiscover_FailClosedOnTagsError(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{
		clustersPages: []eks.ListClustersOutput{{Clusters: []string{"io-foo-a"}}},
		assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
			"io-foo-a": {
				assoc("io-foo-a", "good", "arn-good", "default", "sa-good"),
				assoc("io-foo-a", "throttled", "arn-throttled", "default", "sa-throttled"),
			},
		},
		tagsByARN:    map[string]map[string]string{"arn-good": {}},
		tagsErrByARN: map[string]error{"arn-throttled": errors.New("Throttling")},
	}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (throttled skipped)", len(got))
	}
	if got[0].Identity.NativeIDs["association_id"] != "good" {
		t.Errorf("kept the wrong association: %+v", got[0].Identity.NativeIDs)
	}
}

// blockingEKSPodIdentityClient mirrors blockingDynamoClient. Used for
// concurrency + cancellation tests.
type blockingEKSPodIdentityClient struct {
	clustersPages   []eks.ListClustersOutput
	assocsByCluster map[string][]ekstypes.PodIdentityAssociationSummary
	tagsByARN       map[string]map[string]string

	release chan struct{}

	mu          sync.Mutex
	inflight    int
	maxInflight int
	starts      chan string

	listIdx int
}

func (c *blockingEKSPodIdentityClient) ListClusters(_ context.Context, _ *eks.ListClustersInput, _ ...func(*eks.Options)) (*eks.ListClustersOutput, error) {
	if c.listIdx >= len(c.clustersPages) {
		return &eks.ListClustersOutput{}, nil
	}
	out := c.clustersPages[c.listIdx]
	c.listIdx++
	return &out, nil
}

func (c *blockingEKSPodIdentityClient) ListPodIdentityAssociations(_ context.Context, in *eks.ListPodIdentityAssociationsInput, _ ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error) {
	cluster := aws.ToString(in.ClusterName)
	return &eks.ListPodIdentityAssociationsOutput{Associations: c.assocsByCluster[cluster]}, nil
}

func (c *blockingEKSPodIdentityClient) DescribePodIdentityAssociation(_ context.Context, _ *eks.DescribePodIdentityAssociationInput, _ ...func(*eks.Options)) (*eks.DescribePodIdentityAssociationOutput, error) {
	return nil, errors.New("blockingEKSPodIdentityClient.DescribePodIdentityAssociation: not used in concurrency tests")
}

func (c *blockingEKSPodIdentityClient) ListTagsForResource(ctx context.Context, in *eks.ListTagsForResourceInput, _ ...func(*eks.Options)) (*eks.ListTagsForResourceOutput, error) {
	arn := aws.ToString(in.ResourceArn)
	c.mu.Lock()
	c.inflight++
	if c.inflight > c.maxInflight {
		c.maxInflight = c.inflight
	}
	c.mu.Unlock()
	if c.starts != nil {
		c.starts <- arn
	}
	defer func() {
		c.mu.Lock()
		c.inflight--
		c.mu.Unlock()
	}()
	select {
	case <-c.release:
		return &eks.ListTagsForResourceOutput{Tags: c.tagsByARN[arn]}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestEKSPodIdentityDiscover_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	const total = 30
	const limit = 4

	assocs := make([]ekstypes.PodIdentityAssociationSummary, total)
	tags := make(map[string]map[string]string, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("a-%d", i)
		arn := fmt.Sprintf("arn:aws:eks:us-east-1:123:podidentityassociation/io-foo/%s", id)
		assocs[i] = assoc("io-foo", id, arn, "default", "sa")
		tags[arn] = map[string]string{"Project": "io-foo"}
	}
	release := make(chan struct{})
	bc := &blockingEKSPodIdentityClient{
		clustersPages:   []eks.ListClustersOutput{{Clusters: []string{"io-foo"}}},
		assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{"io-foo": assocs},
		tagsByARN:       tags,
		release:         release,
	}
	d := &eksPodIdentityDiscoverer{
		new:            func(_ string) eksPodIdentityClient { return bc },
		maxConcurrency: limit,
	}

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

func TestEKSPodIdentityDiscover_ContextCancellationUnblocksSiblings(t *testing.T) {
	t.Parallel()
	const total = 5
	assocs := make([]ekstypes.PodIdentityAssociationSummary, total)
	tags := make(map[string]map[string]string, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("a-%d", i)
		arn := fmt.Sprintf("arn-%d", i)
		assocs[i] = assoc("io-foo", id, arn, "default", "sa")
		tags[arn] = map[string]string{}
	}
	release := make(chan struct{})
	starts := make(chan string, total)
	bc := &blockingEKSPodIdentityClient{
		clustersPages:   []eks.ListClustersOutput{{Clusters: []string{"io-foo"}}},
		assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{"io-foo": assocs},
		tagsByARN:       tags,
		release:         release,
		starts:          starts,
	}
	d := &eksPodIdentityDiscoverer{
		new:            func(_ string) eksPodIdentityClient { return bc },
		maxConcurrency: total,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Discover(ctx, DiscoverArgs{Project: "io-foo", Regions: []string{"us-east-1"}, AccountID: "123"})
		done <- err
	}()

	for i := 0; i < total; i++ {
		select {
		case <-starts:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d goroutines entered ListTagsForResource before timeout", i)
		}
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancelled-context error; got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err=%v, want context.Canceled (wrapped is OK)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Discover did not return after parent ctx cancelled — siblings stuck")
	}
}

func TestEKSPodIdentityDiscover_EmitsServiceStartFinish_PerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeEKSPodIdentityClient{
		"us-east-1": {
			clustersPages: []eks.ListClustersOutput{{Clusters: []string{"io-foo-a"}}},
			assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
				"io-foo-a": {assoc("io-foo-a", "a-1", "arn-east", "default", "sa")},
			},
			tagsByARN: map[string]map[string]string{"arn-east": {}},
		},
		"eu-west-1": {
			clustersPages: []eks.ListClustersOutput{{Clusters: []string{"io-foo-b"}}},
			assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
				"io-foo-b": {assoc("io-foo-b", "b-1", "arn-west", "default", "sa")},
			},
			tagsByARN: map[string]map[string]string{"arn-west": {}},
		},
	}
	d := &eksPodIdentityDiscoverer{new: func(region string) eksPodIdentityClient { return fakes[region] }, maxConcurrency: 4}
	rec := &recordingEmitter{}
	if _, err := d.Discover(context.Background(), DiscoverArgs{
		Project: "io-foo", Regions: []string{"us-east-1", "eu-west-1"}, AccountID: "123", Emitter: rec,
	}); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	finishes := map[string]int{}
	for _, e := range rec.snapshot() {
		switch e.Kind {
		case "service_start":
			if e.Service != "eks_pod_identity" {
				t.Errorf("service_start.service=%q, want eks_pod_identity", e.Service)
			}
			starts[e.Region]++
		case "service_finish":
			if e.Service != "eks_pod_identity" {
				t.Errorf("service_finish.service=%q, want eks_pod_identity", e.Service)
			}
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

func TestEKSPodIdentityDiscover_EmitsItemFound_PerAssociation(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{
		clustersPages: []eks.ListClustersOutput{{Clusters: []string{"io-foo-a"}}},
		assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
			"io-foo-a": {
				assoc("io-foo-a", "a", "arn-a", "ns", "sa"),
				assoc("io-foo-a", "b", "arn-b", "ns", "sa"),
			},
		},
		tagsByARN: map[string]map[string]string{"arn-a": {}, "arn-b": {}},
	}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }, maxConcurrency: 4}
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
	for _, it := range items {
		if it.Service != "eks_pod_identity" {
			t.Errorf("item.service=%q, want eks_pod_identity", it.Service)
		}
		if it.TFType != "aws_eks_pod_identity_association" {
			t.Errorf("item.tf_type=%q", it.TFType)
		}
		if !contains2Commas(it.ImportID) {
			t.Errorf("item.import_id=%q is not <cluster>,<id>", it.ImportID)
		}
	}
}

func TestEKSPodIdentityDiscoverByID_AcceptsClusterCommaID(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{
		describeByID: map[string]*ekstypes.PodIdentityAssociation{
			"a-123": {
				ClusterName:    aws.String("io-foo-a"),
				AssociationId:  aws.String("a-123"),
				AssociationArn: aws.String("arn:aws:eks:us-east-1:123:podidentityassociation/io-foo-a/a-123"),
				Namespace:      aws.String("default"),
				ServiceAccount: aws.String("sa"),
			},
		},
	}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }}
	got, err := d.DiscoverByID(context.Background(), "io-foo-a,a-123", "us-east-1", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.Type != "aws_eks_pod_identity_association" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-a/a-123" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.ImportID != "io-foo-a,a-123" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["association_arn"] == "" {
		t.Error("association_arn empty")
	}
}

func TestEKSPodIdentityDiscoverByID_NotFound(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSPodIdentityClient{}
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return fake }}
	_, err := d.DiscoverByID(context.Background(), "io-foo,missing", "us-east-1", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

func TestEKSPodIdentityDiscoverByID_UnsupportedID(t *testing.T) {
	t.Parallel()
	d := &eksPodIdentityDiscoverer{new: func(_ string) eksPodIdentityClient { return &fakeEKSPodIdentityClient{} }}
	cases := []string{
		"",
		"no-comma",
		"a,b,c",
		",empty-cluster",
		"empty-id,",
	}
	for _, id := range cases {
		_, err := d.DiscoverByID(context.Background(), id, "us-east-1", "123")
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("id=%q: err=%v, want ErrNotSupported", id, err)
		}
	}
}

// TestEKSPodIdentityDiscover_MultiRegionTriggersOneSDKCallPerRegion (#291)
// pins the per-region loop. See sqs_test.go for the canonical contract.
func TestEKSPodIdentityDiscover_MultiRegionTriggersOneSDKCallPerRegion(t *testing.T) {
	t.Parallel()
	fakes := map[string]*fakeEKSPodIdentityClient{
		"us-east-1": {
			clustersPages: []eks.ListClustersOutput{{Clusters: []string{"io-foo-east"}}},
			assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
				"io-foo-east": {assoc("io-foo-east", "a-east", "arn-east", "default", "sa")},
			},
			tagsByARN: map[string]map[string]string{"arn-east": {}},
		},
		"eu-west-1": {
			clustersPages: []eks.ListClustersOutput{{Clusters: []string{"io-foo-west"}}},
			assocsByCluster: map[string][]ekstypes.PodIdentityAssociationSummary{
				"io-foo-west": {assoc("io-foo-west", "a-west", "arn-west", "default", "sa")},
			},
			tagsByARN: map[string]map[string]string{"arn-west": {}},
		},
	}
	var seenRegions []string
	d := &eksPodIdentityDiscoverer{new: func(region string) eksPodIdentityClient {
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
	if len(fakes["us-east-1"].listClustersCalls) == 0 {
		t.Error("us-east-1 fake never received ListClusters")
	}
	if len(fakes["eu-west-1"].listClustersCalls) == 0 {
		t.Error("eu-west-1 fake never received ListClusters")
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per region)", len(got))
	}
}
