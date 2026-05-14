package awsdiscover

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// fakeIAMRPAClient implements the iamRolePolicyAttachmentClient
// interface for in-test fakes. The narrow surface (ListRoles +
// ListAttachedRolePolicies) lets per-test seeds drive both the
// parent-enumeration path and the multi-emit FetchItems path
// independently.
//
// The fake records the Marker sent on each call so pagination tests
// can verify the production code propagates Marker correctly between
// pages (a regression that drops `marker = page.Marker` in the
// production loop would re-issue marker=nil and silently re-read
// page 0; the recorded sequence catches that).
type fakeIAMRPAClient struct {
	rolesPages    []*iam.ListRolesOutput
	rolesIdx      int
	rolesErr      error
	rolesMarkers  []string // Marker value seen on each ListRoles call (in order). "" == nil pointer.

	attachedByRole       map[string][]*iam.ListAttachedRolePoliciesOutput
	attachedIdxByRole    map[string]int
	attachedErrByRole    map[string]error
	attachedMarkers      map[string][]string // Marker per ListAttachedRolePolicies call per role
}

func (f *fakeIAMRPAClient) ListRoles(_ context.Context, in *iam.ListRolesInput, _ ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	f.rolesMarkers = append(f.rolesMarkers, aws.ToString(in.Marker))
	if f.rolesErr != nil {
		return nil, f.rolesErr
	}
	if f.rolesIdx >= len(f.rolesPages) {
		return &iam.ListRolesOutput{}, nil
	}
	out := f.rolesPages[f.rolesIdx]
	f.rolesIdx++
	return out, nil
}

func (f *fakeIAMRPAClient) ListAttachedRolePolicies(_ context.Context, in *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	role := aws.ToString(in.RoleName)
	if f.attachedMarkers == nil {
		f.attachedMarkers = map[string][]string{}
	}
	f.attachedMarkers[role] = append(f.attachedMarkers[role], aws.ToString(in.Marker))
	if err, ok := f.attachedErrByRole[role]; ok {
		return nil, err
	}
	pages, ok := f.attachedByRole[role]
	if !ok {
		return &iam.ListAttachedRolePoliciesOutput{}, nil
	}
	if f.attachedIdxByRole == nil {
		f.attachedIdxByRole = map[string]int{}
	}
	idx := f.attachedIdxByRole[role]
	if idx >= len(pages) {
		return &iam.ListAttachedRolePoliciesOutput{}, nil
	}
	f.attachedIdxByRole[role] = idx + 1
	return pages[idx], nil
}

// TestListIAMRoleNamesNonSLR_FiltersServiceLinkedRoles pins the
// SLR-skip behavior: roles whose Path starts with /aws-service-role/
// are filtered out so the per-role attachment fan-out doesn't spam
// AccessDenied warns. The SLR-skip prefix is the well-known
// iamServiceRolePathPrefix constant.
func TestListIAMRoleNamesNonSLR_FiltersServiceLinkedRoles(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMRPAClient{
		rolesPages: []*iam.ListRolesOutput{
			{
				Roles: []iamtypes.Role{
					{RoleName: ptr("regular-role-A"), Path: ptr("/")},
					{RoleName: ptr("regular-role-B"), Path: ptr("/some/custom/path/")},
					{RoleName: ptr("AWSServiceRoleForElasticache"), Path: ptr("/aws-service-role/elasticache.amazonaws.com/")},
					{RoleName: ptr("AWSServiceRoleForRDS"), Path: ptr("/aws-service-role/rds.amazonaws.com/")},
				},
			},
		},
	}
	got, err := listIAMRoleNamesNonSLRWithClient(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"regular-role-A", "regular-role-B"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d (SLRs must be filtered)", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d]=%q, want %q", i, got[i], w)
		}
	}
}

// TestListIAMRoleNamesNonSLR_PaginatesViaMarker pins the
// Marker-driven pagination contract. A two-page response surfaces
// roles from both pages, IsTruncated=false on the second page
// terminates the loop, AND the production loop forwards page-1's
// Marker as the second call's input Marker — without that forwarding
// step the SDK would re-read page 0 and the test would still pass
// against a 2-element checkpoint but mis-emit the role names. The
// rolesMarkers assertion catches that regression.
func TestListIAMRoleNamesNonSLR_PaginatesViaMarker(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMRPAClient{
		rolesPages: []*iam.ListRolesOutput{
			{
				Roles: []iamtypes.Role{
					{RoleName: ptr("role-1"), Path: ptr("/")},
				},
				IsTruncated: true,
				Marker:      ptr("page-2"),
			},
			{
				Roles: []iamtypes.Role{
					{RoleName: ptr("role-2"), Path: ptr("/")},
				},
				IsTruncated: false,
			},
		},
	}
	got, err := listIAMRoleNamesNonSLRWithClient(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (both pages drained)", len(got))
	}
	wantMarkers := []string{"", "page-2"}
	if !reflect.DeepEqual(fake.rolesMarkers, wantMarkers) {
		t.Errorf("rolesMarkers=%v, want %v (Marker must propagate page-N -> page-N+1)", fake.rolesMarkers, wantMarkers)
	}
}

// TestListIAMRoleNamesNonSLR_EmptyAccountReturnsNonNil pins the #255
// JSON-shape contract: zero roles surface as a non-nil empty slice.
func TestListIAMRoleNamesNonSLR_EmptyAccountReturnsNonNil(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMRPAClient{rolesPages: nil}
	got, err := listIAMRoleNamesNonSLRWithClient(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("got=nil, want non-nil empty slice (#255 JSON-shape contract)")
	}
}

// TestFetchIAMRolePolicyAttachments_MultipleAttachmentsEmitOnePerPolicy
// pins the core multi-emit semantics: one role with N attached managed
// policies yields N TF-resource emissions, each with the canonical
// "<role>/<policy_arn>" import ID.
func TestFetchIAMRolePolicyAttachments_MultipleAttachmentsEmitOnePerPolicy(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMRPAClient{
		attachedByRole: map[string][]*iam.ListAttachedRolePoliciesOutput{
			"my-role": {
				{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyName: ptr("AmazonS3ReadOnlyAccess"), PolicyArn: ptr("arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess")},
						{PolicyName: ptr("CloudWatchLogsFullAccess"), PolicyArn: ptr("arn:aws:iam::aws:policy/CloudWatchLogsFullAccess")},
					},
				},
			},
		},
	}
	got, err := fetchIAMRolePolicyAttachmentsWithClient(context.Background(), fake, "my-role")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (one per attached policy)", len(got))
	}
	wantImports := map[string]bool{
		"my-role/arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess":   false,
		"my-role/arn:aws:iam::aws:policy/CloudWatchLogsFullAccess": false,
	}
	for _, e := range got {
		if _, ok := wantImports[e.ImportID]; ok {
			wantImports[e.ImportID] = true
		}
		if e.NativeIDs["role"] != "my-role" {
			t.Errorf("NativeIDs[role]=%q, want my-role", e.NativeIDs["role"])
		}
		if e.NativeIDs["policy_arn"] == "" {
			t.Errorf("NativeIDs[policy_arn] is empty for emission %s", e.ImportID)
		}
	}
	for k, ok := range wantImports {
		if !ok {
			t.Errorf("expected emission with ImportID=%q", k)
		}
	}
}

// TestFetchIAMRolePolicyAttachments_NoAttachmentsEmitsZero pins the
// zero-attachment case: a role with no managed-policy attachments
// yields an empty slice (not nil — JSON-shape contract).
func TestFetchIAMRolePolicyAttachments_NoAttachmentsEmitsZero(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMRPAClient{}
	got, err := fetchIAMRolePolicyAttachmentsWithClient(context.Background(), fake, "lonely-role")
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

// TestFetchIAMRolePolicyAttachments_NoSuchEntitySwallowed pins the
// race posture: a role that vanished between ListRoles and the
// FetchItems call surfaces as zero emissions rather than a propagated
// error. NoSuchEntity / NoSuchEntityException are both swallowed
// because IAM's error code shape has varied across SDK releases.
func TestFetchIAMRolePolicyAttachments_NoSuchEntitySwallowed(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"NoSuchEntity", "NoSuchEntityException"} {
		fake := &fakeIAMRPAClient{
			attachedErrByRole: map[string]error{
				"vanished-role": fakeAPIErr(code, "role gone"),
			},
		}
		got, err := fetchIAMRolePolicyAttachmentsWithClient(context.Background(), fake, "vanished-role")
		if err != nil {
			t.Errorf("%s: err=%v, want nil (swallowed)", code, err)
		}
		if len(got) != 0 {
			t.Errorf("%s: len=%d, want 0", code, len(got))
		}
	}
}

// TestFetchIAMRolePolicyAttachments_AccessDeniedPropagates pins that
// AccessDenied (not a NotFound code) propagates so the bulk Discover
// path's per-parent soft-fail can convert it to a ServiceWarn. The
// errors.Is assertion catches a regression that wraps the SDK error
// as a different sentinel.
func TestFetchIAMRolePolicyAttachments_AccessDeniedPropagates(t *testing.T) {
	t.Parallel()
	seedErr := fakeAPIErr("AccessDenied", "no perms")
	fake := &fakeIAMRPAClient{
		attachedErrByRole: map[string]error{"my-role": seedErr},
	}
	_, err := fetchIAMRolePolicyAttachmentsWithClient(context.Background(), fake, "my-role")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err does not wrap seedErr: got %v", err)
	}
}

// TestFetchIAMRolePolicyAttachments_PaginatesViaMarker pins the
// multi-page contract: a role with >1 page of attachments drains
// every page, AND the production loop forwards page-1's Marker as the
// second call's input Marker. Asserting attachedMarkers catches the
// regression where the production loop drops `marker = page.Marker`
// and silently re-reads page 0 (which would still produce a
// 2-emission count if the fake didn't track call order).
func TestFetchIAMRolePolicyAttachments_PaginatesViaMarker(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMRPAClient{
		attachedByRole: map[string][]*iam.ListAttachedRolePoliciesOutput{
			"big-role": {
				{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyName: ptr("p1"), PolicyArn: ptr("arn:aws:iam::aws:policy/p1")},
					},
					IsTruncated: true,
					Marker:      ptr("page-2"),
				},
				{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyName: ptr("p2"), PolicyArn: ptr("arn:aws:iam::aws:policy/p2")},
					},
					IsTruncated: false,
				},
			},
		},
	}
	got, err := fetchIAMRolePolicyAttachmentsWithClient(context.Background(), fake, "big-role")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d, want 2 (both pages drained)", len(got))
	}
	wantMarkers := []string{"", "page-2"}
	if !reflect.DeepEqual(fake.attachedMarkers["big-role"], wantMarkers) {
		t.Errorf("attachedMarkers[big-role]=%v, want %v (Marker must propagate page-N -> page-N+1)", fake.attachedMarkers["big-role"], wantMarkers)
	}
}

// TestFetchIAMRolePolicyAttachments_EmptyPolicyArnSkipped pins that a
// malformed AttachedPolicy entry with no PolicyArn is silently
// skipped rather than emitting an unaddressable resource.
func TestFetchIAMRolePolicyAttachments_EmptyPolicyArnSkipped(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMRPAClient{
		attachedByRole: map[string][]*iam.ListAttachedRolePoliciesOutput{
			"my-role": {
				{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyName: ptr("real-policy"), PolicyArn: ptr("arn:aws:iam::aws:policy/real")},
						{PolicyName: ptr("broken-policy"), PolicyArn: nil},
					},
				},
			},
		},
	}
	got, err := fetchIAMRolePolicyAttachmentsWithClient(context.Background(), fake, "my-role")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("len=%d, want 1 (entry with nil PolicyArn must be skipped)", len(got))
	}
}

// TestListIAMRoleNamesNonSLR_PropagatesError pins that a real
// ListRoles error surfaces wrapped so the discoverer's per-region
// abort path identifies the source.
func TestListIAMRoleNamesNonSLR_PropagatesError(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("list-roles-seed")
	fake := &fakeIAMRPAClient{rolesErr: seedErr}
	_, err := listIAMRoleNamesNonSLRWithClient(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err=%v, want errors.Is(err, seedErr)", err)
	}
}

// TestNewIAMRPAClient_ProductionFactoryReturnsRealClient pins the
// production factory: a real *iam.Client (not nil), constructed from
// the supplied aws.Config.
func TestNewIAMRPAClient_ProductionFactoryReturnsRealClient(t *testing.T) {
	t.Parallel()
	c := newIAMRPAClient(aws.Config{Region: "us-east-1"}, "")
	if c == nil {
		t.Fatal("newIAMRPAClient returned nil")
	}
}
