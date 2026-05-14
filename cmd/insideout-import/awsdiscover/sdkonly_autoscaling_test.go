package awsdiscover

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

// fakeASGTagClient implements asgTagClient for in-test fakes. Each
// DescribeAutoScalingGroups response is queueable per "call shape"
// (filtered-by-names vs unfiltered) so the ListParents and FetchItems
// paths can be seeded independently.
type fakeASGTagClient struct {
	allGroups   *autoscaling.DescribeAutoScalingGroupsOutput
	groupByName map[string]*autoscaling.DescribeAutoScalingGroupsOutput
	errOnAll    error
	errOnByName map[string]error
	callsAll    int
	callsByName map[string]int
}

func (f *fakeASGTagClient) DescribeAutoScalingGroups(_ context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	if len(in.AutoScalingGroupNames) > 0 {
		name := in.AutoScalingGroupNames[0]
		if err, ok := f.errOnByName[name]; ok {
			return nil, err
		}
		if f.callsByName == nil {
			f.callsByName = map[string]int{}
		}
		f.callsByName[name]++
		if out, ok := f.groupByName[name]; ok {
			return out, nil
		}
		return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
	}
	if f.errOnAll != nil {
		return nil, f.errOnAll
	}
	f.callsAll++
	if f.allGroups != nil {
		return f.allGroups, nil
	}
	return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
}

// TestListASGNames_HappyPath pins the parent-enumeration contract:
// names surface in the order the SDK returned, deterministically
// (the discoverer re-sorts on emit so any order is acceptable; the
// lister's job is to pass through).
func TestListASGNames_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeASGTagClient{
		allGroups: &autoscaling.DescribeAutoScalingGroupsOutput{
			AutoScalingGroups: []astypes.AutoScalingGroup{
				{AutoScalingGroupName: ptr("asg-web"), AvailabilityZones: []string{"us-east-1a"}},
				{AutoScalingGroupName: ptr("asg-worker"), AvailabilityZones: []string{"us-east-1a"}},
			},
		},
	}
	got, err := listASGNamesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"asg-web", "asg-worker"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d]=%q, want %q", i, got[i], w)
		}
	}
}

// TestListASGNames_EmptyAccountReturnsNonNil pins the #255 JSON-shape
// contract.
func TestListASGNames_EmptyAccountReturnsNonNil(t *testing.T) {
	t.Parallel()
	fake := &fakeASGTagClient{}
	got, err := listASGNamesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("got=nil, want non-nil empty slice")
	}
}

// TestListASGNames_PropagatesError pins the wrap-and-surface contract
// for the per-region abort path.
func TestListASGNames_PropagatesError(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("describe-asg-seed")
	fake := &fakeASGTagClient{errOnAll: seedErr}
	_, err := listASGNamesWithClient(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err=%v, want errors.Is(err, seedErr)", err)
	}
}

// TestFetchASGTags_MultipleTagsEmitOnePerKey pins the core multi-emit
// semantics: one ASG with N tags yields N TF-resource emissions, each
// with the canonical "<asg_name>,<tag_key>" import ID.
func TestFetchASGTags_MultipleTagsEmitOnePerKey(t *testing.T) {
	t.Parallel()
	asg := "asg-web"
	fake := &fakeASGTagClient{
		groupByName: map[string]*autoscaling.DescribeAutoScalingGroupsOutput{
			asg: {
				AutoScalingGroups: []astypes.AutoScalingGroup{
					{
						AutoScalingGroupName: ptr(asg),
						AvailabilityZones:    []string{"us-east-1a"},
						Tags: []astypes.TagDescription{
							{Key: ptr("Environment"), Value: ptr("prod"), ResourceId: ptr(asg)},
							{Key: ptr("Owner"), Value: ptr("platform"), ResourceId: ptr(asg)},
							{Key: ptr("Project"), Value: ptr("io-abc"), ResourceId: ptr(asg)},
						},
					},
				},
			},
		},
	}
	got, err := fetchASGTagsWithClient(context.Background(), fake, asg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (one per tag key)", len(got))
	}
	wantImports := map[string]bool{
		asg + ",Environment": false,
		asg + ",Owner":       false,
		asg + ",Project":     false,
	}
	for _, e := range got {
		if _, ok := wantImports[e.ImportID]; ok {
			wantImports[e.ImportID] = true
		}
		if e.NativeIDs["autoscaling_group_name"] != asg {
			t.Errorf("NativeIDs[autoscaling_group_name]=%q, want %s", e.NativeIDs["autoscaling_group_name"], asg)
		}
		if e.NativeIDs["key"] == "" {
			t.Errorf("NativeIDs[key] empty for emission %s", e.ImportID)
		}
	}
	for k, ok := range wantImports {
		if !ok {
			t.Errorf("expected emission with ImportID=%q", k)
		}
	}
}

// TestFetchASGTags_NoTagsEmitsZero pins that an ASG with no tags
// yields a non-nil empty slice (JSON-shape contract). Untagged ASGs
// are common in dev accounts.
func TestFetchASGTags_NoTagsEmitsZero(t *testing.T) {
	t.Parallel()
	asg := "untagged-asg"
	fake := &fakeASGTagClient{
		groupByName: map[string]*autoscaling.DescribeAutoScalingGroupsOutput{
			asg: {
				AutoScalingGroups: []astypes.AutoScalingGroup{
					{AutoScalingGroupName: ptr(asg), AvailabilityZones: []string{"us-east-1a"}},
				},
			},
		},
	}
	got, err := fetchASGTagsWithClient(context.Background(), fake, asg)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("got=nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

// TestFetchASGTags_VanishedASGEmitsZero pins the race-with-deletion
// case: DescribeAutoScalingGroups returns an empty AutoScalingGroups
// slice for a vanished ASG (no NotFound error). The FetchItems closure
// emits zero rather than erroring.
func TestFetchASGTags_VanishedASGEmitsZero(t *testing.T) {
	t.Parallel()
	fake := &fakeASGTagClient{
		groupByName: map[string]*autoscaling.DescribeAutoScalingGroupsOutput{
			"missing-asg": {AutoScalingGroups: nil},
		},
	}
	got, err := fetchASGTagsWithClient(context.Background(), fake, "missing-asg")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0 (vanished ASG produces no tag emissions)", len(got))
	}
}

// TestFetchASGTags_EmptyKeySkipped pins that a TagDescription with an
// empty Key is silently dropped — emitting a TF resource with an empty
// key half of the import ID would be unaddressable.
func TestFetchASGTags_EmptyKeySkipped(t *testing.T) {
	t.Parallel()
	asg := "asg"
	fake := &fakeASGTagClient{
		groupByName: map[string]*autoscaling.DescribeAutoScalingGroupsOutput{
			asg: {
				AutoScalingGroups: []astypes.AutoScalingGroup{
					{
						AutoScalingGroupName: ptr(asg),
						AvailabilityZones:    []string{"us-east-1a"},
						Tags: []astypes.TagDescription{
							{Key: ptr(""), Value: ptr("ignored")},
							{Key: ptr("RealKey"), Value: ptr("kept")},
						},
					},
				},
			},
		},
	}
	got, err := fetchASGTagsWithClient(context.Background(), fake, asg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("len=%d, want 1 (empty-key entry must be skipped)", len(got))
	}
}

// TestFetchASGTags_PropagatesError pins that an SDK error on the
// per-ASG describe call surfaces so the bulk Discover path can
// ServiceWarn it. errors.Is on the seed catches a regression that
// wraps the SDK error as a different sentinel or silently swallows
// it.
func TestFetchASGTags_PropagatesError(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("describe-by-name-seed")
	fake := &fakeASGTagClient{
		errOnByName: map[string]error{"asg-X": seedErr},
	}
	_, err := fetchASGTagsWithClient(context.Background(), fake, "asg-X")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err does not wrap seedErr: got %v", err)
	}
}

// TestNewASGTagClient_ProductionFactoryReturnsRealClient pins the
// production factory.
func TestNewASGTagClient_ProductionFactoryReturnsRealClient(t *testing.T) {
	t.Parallel()
	c := newASGTagClient(aws.Config{Region: "us-east-1"}, "us-east-1")
	if c == nil {
		t.Fatal("newASGTagClient returned nil")
	}
}
