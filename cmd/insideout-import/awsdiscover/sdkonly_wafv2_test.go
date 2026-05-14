package awsdiscover

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"
)

// fakeWAFv2AssociationClient implements wafv2AssociationClient for
// in-test fakes. ListWebACLs responses are keyed by Scope so the test
// can seed REGIONAL and CLOUDFRONT ACLs independently;
// ListResourcesForWebACL responses are keyed by (webACLArn, ResourceType)
// to drive the per-type fan-out.
type fakeWAFv2AssociationClient struct {
	listByScope    map[wafv2types.Scope]*wafv2.ListWebACLsOutput
	listScopeErr   map[wafv2types.Scope]error
	resourcesByKey map[string]*wafv2.ListResourcesForWebACLOutput
	resourcesErr   map[string]error
}

func (f *fakeWAFv2AssociationClient) ListWebACLs(_ context.Context, in *wafv2.ListWebACLsInput, _ ...func(*wafv2.Options)) (*wafv2.ListWebACLsOutput, error) {
	if err, ok := f.listScopeErr[in.Scope]; ok {
		return nil, err
	}
	if out, ok := f.listByScope[in.Scope]; ok {
		return out, nil
	}
	return &wafv2.ListWebACLsOutput{}, nil
}

func wafv2Key(arn string, rt wafv2types.ResourceType) string {
	return arn + "|" + string(rt)
}

func (f *fakeWAFv2AssociationClient) ListResourcesForWebACL(_ context.Context, in *wafv2.ListResourcesForWebACLInput, _ ...func(*wafv2.Options)) (*wafv2.ListResourcesForWebACLOutput, error) {
	k := wafv2Key(aws.ToString(in.WebACLArn), in.ResourceType)
	if err, ok := f.resourcesErr[k]; ok {
		return nil, err
	}
	if out, ok := f.resourcesByKey[k]; ok {
		return out, nil
	}
	return &wafv2.ListResourcesForWebACLOutput{}, nil
}

// TestListWAFv2WebACLs_RegionalOnlyNonUSEast1 pins that listing WebACLs
// from any region other than us-east-1 enumerates only REGIONAL scope —
// CLOUDFRONT scope is documented as us-east-1-only by AWS and a call
// against it from another region would error.
func TestListWAFv2WebACLs_RegionalOnlyNonUSEast1(t *testing.T) {
	t.Parallel()
	fake := &fakeWAFv2AssociationClient{
		listByScope: map[wafv2types.Scope]*wafv2.ListWebACLsOutput{
			wafv2types.ScopeRegional: {
				WebACLs: []wafv2types.WebACLSummary{
					{ARN: ptr("arn:aws:wafv2:eu-west-1:1:regional/webacl/regional-acl/uuid")},
				},
			},
			// Seed CLOUDFRONT too — the discoverer should NOT request it
			// from a non-us-east-1 region. If it did, this entry would
			// leak in.
			wafv2types.ScopeCloudfront: {
				WebACLs: []wafv2types.WebACLSummary{
					{ARN: ptr("arn:aws:wafv2:global:1:global/webacl/cf-acl/uuid")},
				},
			},
		},
	}
	got, err := listWAFv2WebACLsWithClient(context.Background(), fake, "eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1 (REGIONAL only outside us-east-1)", len(got))
	}
	if !strings.Contains(got[0], "regional-acl") {
		t.Errorf("got[0]=%q, want REGIONAL ACL", got[0])
	}
}

// TestListWAFv2WebACLs_USEast1IncludesCloudfront pins the dual-scope
// enumeration in us-east-1: both REGIONAL and CLOUDFRONT-scoped ACLs
// surface. Both calls are issued in sequence; the result concatenates.
func TestListWAFv2WebACLs_USEast1IncludesCloudfront(t *testing.T) {
	t.Parallel()
	fake := &fakeWAFv2AssociationClient{
		listByScope: map[wafv2types.Scope]*wafv2.ListWebACLsOutput{
			wafv2types.ScopeRegional: {
				WebACLs: []wafv2types.WebACLSummary{
					{ARN: ptr("arn:aws:wafv2:us-east-1:1:regional/webacl/r1/uuid")},
				},
			},
			wafv2types.ScopeCloudfront: {
				WebACLs: []wafv2types.WebACLSummary{
					{ARN: ptr("arn:aws:wafv2:global:1:global/webacl/cf1/uuid")},
				},
			},
		},
	}
	got, err := listWAFv2WebACLsWithClient(context.Background(), fake, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (REGIONAL + CLOUDFRONT in us-east-1)", len(got))
	}
}

// TestListWAFv2WebACLs_PropagatesScopeError pins that an error from
// the per-scope ListWebACLs call wraps and surfaces — the discoverer
// can't proceed without a parent list.
func TestListWAFv2WebACLs_PropagatesScopeError(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("regional-scope-seed")
	fake := &fakeWAFv2AssociationClient{
		listScopeErr: map[wafv2types.Scope]error{
			wafv2types.ScopeRegional: seedErr,
		},
	}
	_, err := listWAFv2WebACLsWithClient(context.Background(), fake, "us-east-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err=%v, want errors.Is(err, seedErr)", err)
	}
}

// TestFetchWAFv2WebACLAssociations_MultiTypeFanOut pins the core
// multi-emit semantics: one WebACL across multiple resource types
// yields one emission per (resource_arn, web_acl_arn) pair. The
// import format is "<resource_arn>,<web_acl_arn>".
func TestFetchWAFv2WebACLAssociations_MultiTypeFanOut(t *testing.T) {
	t.Parallel()
	acl := "arn:aws:wafv2:us-east-1:1:regional/webacl/acl/uuid"
	alb := "arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/app/lb/uuid"
	api := "arn:aws:apigateway:us-east-1::/restapis/r1/stages/prod"
	fake := &fakeWAFv2AssociationClient{
		resourcesByKey: map[string]*wafv2.ListResourcesForWebACLOutput{
			wafv2Key(acl, wafv2types.ResourceTypeApplicationLoadBalancer): {ResourceArns: []string{alb}},
			wafv2Key(acl, wafv2types.ResourceTypeApiGateway):              {ResourceArns: []string{api}},
		},
	}
	got, err := fetchWAFv2WebACLAssociationsWithClient(context.Background(), fake, acl)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (ALB + API Gateway)", len(got))
	}
	for _, e := range got {
		if !strings.HasSuffix(e.ImportID, ","+acl) {
			t.Errorf("ImportID=%q, want suffix ',%s'", e.ImportID, acl)
		}
		if e.NativeIDs["web_acl_arn"] != acl {
			t.Errorf("NativeIDs[web_acl_arn]=%q, want %s", e.NativeIDs["web_acl_arn"], acl)
		}
	}
}

// TestFetchWAFv2WebACLAssociations_NoAssociationsEmitsZero pins the
// no-associations path: zero ResourceArns across the entire matrix
// yields zero emissions (an empty but non-nil slice).
func TestFetchWAFv2WebACLAssociations_NoAssociationsEmitsZero(t *testing.T) {
	t.Parallel()
	fake := &fakeWAFv2AssociationClient{}
	got, err := fetchWAFv2WebACLAssociationsWithClient(context.Background(), fake, "arn:aws:wafv2:us-east-1:1:regional/webacl/lonely/uuid")
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

// TestFetchWAFv2WebACLAssociations_PerTypeInvalidParameterSwallowed
// pins the per-resource-type soft-fail: a
// WAFInvalidParameterException for one resource type (e.g. Verified
// Access in a region without the service) must NOT abort the whole
// fan-out — emissions from other resource types still surface.
func TestFetchWAFv2WebACLAssociations_PerTypeInvalidParameterSwallowed(t *testing.T) {
	t.Parallel()
	acl := "arn:aws:wafv2:us-east-1:1:regional/webacl/acl/uuid"
	alb := "arn:aws:elasticloadbalancing:us-east-1:1:loadbalancer/app/lb/uuid"
	fake := &fakeWAFv2AssociationClient{
		resourcesByKey: map[string]*wafv2.ListResourcesForWebACLOutput{
			wafv2Key(acl, wafv2types.ResourceTypeApplicationLoadBalancer): {ResourceArns: []string{alb}},
		},
		resourcesErr: map[string]error{
			wafv2Key(acl, wafv2types.ResourceTypeVerifiedAccessInstance): fakeAPIErr("WAFInvalidParameterException", "VA not supported here"),
		},
	}
	got, err := fetchWAFv2WebACLAssociationsWithClient(context.Background(), fake, acl)
	if err != nil {
		t.Fatalf("err=%v, want nil (per-type errors swallowed)", err)
	}
	if len(got) != 1 {
		t.Errorf("len=%d, want 1 (ALB emitted, VA per-type error swallowed)", len(got))
	}
}

// TestFetchWAFv2WebACLAssociations_PerTypeAccessDeniedPropagates pins
// that errors OTHER than the documented "type not supported here"
// codes (WAFInvalidParameterException / ValidationException) propagate
// — AccessDenied indicates a real permissions gap the operator must
// see.
func TestFetchWAFv2WebACLAssociations_PerTypeAccessDeniedPropagates(t *testing.T) {
	t.Parallel()
	acl := "arn:aws:wafv2:us-east-1:1:regional/webacl/acl/uuid"
	fake := &fakeWAFv2AssociationClient{
		resourcesErr: map[string]error{
			wafv2Key(acl, wafv2types.ResourceTypeApplicationLoadBalancer): fakeAPIErr("AccessDeniedException", "no perms"),
		},
	}
	_, err := fetchWAFv2WebACLAssociationsWithClient(context.Background(), fake, acl)
	if err == nil {
		t.Fatal("expected error (AccessDenied must not be swallowed)")
	}
}

// TestNewWAFv2AssociationClient_ProductionFactoryReturnsRealClient
// pins the production factory: a real *wafv2.Client (not nil),
// constructed from the supplied aws.Config and pinned to the given
// region.
func TestNewWAFv2AssociationClient_ProductionFactoryReturnsRealClient(t *testing.T) {
	t.Parallel()
	c := newWAFv2AssociationClient(aws.Config{Region: "us-east-1"}, "us-east-1")
	if c == nil {
		t.Fatal("newWAFv2AssociationClient returned nil")
	}
}

// TestWAFv2RegionalAssociationResourceTypes_NonEmpty pins the
// resource-type matrix; a future refactor that drops the table must
// also update the per-type fan-out logic in FetchItems. Without this
// guard a misordered diff could silently zero out the matrix.
func TestWAFv2RegionalAssociationResourceTypes_NonEmpty(t *testing.T) {
	t.Parallel()
	if len(wafv2RegionalAssociationResourceTypes) == 0 {
		t.Fatal("wafv2RegionalAssociationResourceTypes is empty; the per-type fan-out would emit zero items for every WebACL")
	}
}
