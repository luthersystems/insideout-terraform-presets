package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cogniidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// This file holds unit tests for the SDK-backed listers in
// cloudcontrol_listers.go. Each lister is exercised end-to-end through
// its narrow client interface with a hand-rolled fake — the pagination
// loops, JSON-shape of the emitted ResourceModel strings, and
// nil-vs-empty semantics on empty responses are all load-bearing for
// the discoverer's downstream branches.

// fakeCognitoUserPoolsLister is a hand-rolled fake satisfying the
// cognitoUserPoolsLister interface. listPages drives the ListUserPools
// pagination; describeByID drives DescribeUserPool (used by the
// UserPoolDomain walker).
type fakeCognitoUserPoolsLister struct {
	listPages       []cognitoidentityprovider.ListUserPoolsOutput
	listCalls       int
	listErr         error
	describeByID    map[string]cognitoidentityprovider.DescribeUserPoolOutput
	describeCalls   int
	describeErr     error
	describeErrFor  string // when set, DescribeUserPool returns describeErr only for this pool ID
	describeCallIDs []string
}

func (f *fakeCognitoUserPoolsLister) ListUserPools(_ context.Context, _ *cognitoidentityprovider.ListUserPoolsInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListUserPoolsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		// Exhausted: return empty no-token page so the paginator stops.
		return &cognitoidentityprovider.ListUserPoolsOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func (f *fakeCognitoUserPoolsLister) DescribeUserPool(_ context.Context, in *cognitoidentityprovider.DescribeUserPoolInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolOutput, error) {
	id := aws.ToString(in.UserPoolId)
	f.describeCalls++
	f.describeCallIDs = append(f.describeCallIDs, id)
	if f.describeErrFor != "" && f.describeErrFor == id {
		return nil, f.describeErr
	}
	if f.describeErr != nil && f.describeErrFor == "" {
		return nil, f.describeErr
	}
	out, ok := f.describeByID[id]
	if !ok {
		return &cognitoidentityprovider.DescribeUserPoolOutput{}, nil
	}
	return &out, nil
}

// cognitoListPage builds a ListUserPoolsOutput with the given pool IDs
// and an optional NextToken. Used to construct multi-page fixtures.
func cognitoListPage(token string, ids ...string) cognitoidentityprovider.ListUserPoolsOutput {
	descs := make([]cogniidptypes.UserPoolDescriptionType, 0, len(ids))
	for _, id := range ids {
		descs = append(descs, cogniidptypes.UserPoolDescriptionType{Id: aws.String(id)})
	}
	out := cognitoidentityprovider.ListUserPoolsOutput{UserPools: descs}
	if token != "" {
		out.NextToken = aws.String(token)
	}
	return out
}

// TestListCognitoUserPools_PaginatesAndReturnsModels pins the
// pool-enumeration helper used by aws_cognito_user_pool_client's
// ParentLister: every pool across every page must surface as a
// well-formed JSON ResourceModel string with UserPoolId set. A
// regression that drops a page or emits malformed JSON would
// silently produce zero child clients.
func TestListCognitoUserPools_PaginatesAndReturnsModels(t *testing.T) {
	t.Parallel()
	fake := &fakeCognitoUserPoolsLister{
		listPages: []cognitoidentityprovider.ListUserPoolsOutput{
			cognitoListPage("tok1", "us-east-1_AAA", "us-east-1_BBB"),
			cognitoListPage("tok2", "us-east-1_CCC"),
			cognitoListPage("", "us-east-1_DDD", "us-east-1_EEE"),
		},
	}
	got, err := listCognitoUserPoolsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("listCognitoUserPools returned nil — must be non-nil per the discoverer's len(parentModels)==0 contract")
	}
	want := []string{
		`{"UserPoolId":"us-east-1_AAA"}`,
		`{"UserPoolId":"us-east-1_BBB"}`,
		`{"UserPoolId":"us-east-1_CCC"}`,
		`{"UserPoolId":"us-east-1_DDD"}`,
		`{"UserPoolId":"us-east-1_EEE"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 3 {
		t.Errorf("listCalls=%d, want 3 (paginated)", fake.listCalls)
	}
	// Every emitted model must be parseable JSON with UserPoolId set —
	// the CC schema requires this exact key.
	for _, m := range got {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(m), &parsed); err != nil {
			t.Errorf("emitted model %q is not valid JSON: %v", m, err)
		}
		if _, ok := parsed["UserPoolId"]; !ok {
			t.Errorf("model %q missing UserPoolId key", m)
		}
	}
}

// TestListCognitoUserPools_EmptyAccountReturnsNonNilEmpty pins the
// non-nil-empty contract: an account with zero user pools must return
// an empty slice (not nil) so the discoverer's len-check short-circuits
// cleanly rather than treating "no pools" as "ParentLister not set."
func TestListCognitoUserPools_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeCognitoUserPoolsLister{
		listPages: []cognitoidentityprovider.ListUserPoolsOutput{
			cognitoListPage(""),
		},
	}
	got, err := listCognitoUserPoolsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty-account result must be non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

// TestListCognitoUserPools_PropagatesListError pins that an SDK error
// on ListUserPools surfaces via errors.Is rather than being swallowed
// or rewrapped — symmetric with the discoverer's ListResources error
// path.
func TestListCognitoUserPools_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: cognito-idp:ListUserPools")
	fake := &fakeCognitoUserPoolsLister{listErr: sentinel}
	_, err := listCognitoUserPoolsWithClient(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

// fakeLambdaFunctionsLister is a hand-rolled fake satisfying the
// lambdaFunctionsLister interface.
type fakeLambdaFunctionsLister struct {
	listPages []lambda.ListFunctionsOutput
	listCalls int
	listErr   error
}

func (f *fakeLambdaFunctionsLister) ListFunctions(_ context.Context, _ *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &lambda.ListFunctionsOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func lambdaListPage(marker string, fnNames ...string) lambda.ListFunctionsOutput {
	fns := make([]lambdatypes.FunctionConfiguration, 0, len(fnNames))
	for _, n := range fnNames {
		fns = append(fns, lambdatypes.FunctionConfiguration{FunctionName: aws.String(n)})
	}
	out := lambda.ListFunctionsOutput{Functions: fns}
	if marker != "" {
		out.NextMarker = aws.String(marker)
	}
	return out
}

// TestListLambdaFunctions_PaginatesAndReturnsModels mirrors the
// Cognito-pool pagination pin for the Lambda alias ParentLister's
// upstream enumeration: every function across every page surfaces as
// a JSON ResourceModel with FunctionName set.
func TestListLambdaFunctions_PaginatesAndReturnsModels(t *testing.T) {
	t.Parallel()
	fake := &fakeLambdaFunctionsLister{
		listPages: []lambda.ListFunctionsOutput{
			lambdaListPage("m1", "fn-alpha", "fn-beta"),
			lambdaListPage("", "fn-gamma"),
		},
	}
	got, err := listLambdaFunctionsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`{"FunctionName":"fn-alpha"}`,
		`{"FunctionName":"fn-beta"}`,
		`{"FunctionName":"fn-gamma"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2", fake.listCalls)
	}
}

// TestWAFv2ParentModels_CloudFrontOnlyInUSEast1 pins the
// region-conditional scope behavior: WAFv2's CLOUDFRONT scope is only
// valid at the us-east-1 endpoint. Returning CLOUDFRONT from
// eu-west-1 would cause CC ListResources to fail with
// InvalidRequestException.
func TestWAFv2ParentModels_CloudFrontOnlyInUSEast1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		region string
		want   []string
	}{
		{region: "us-east-1", want: []string{`{"Scope":"REGIONAL"}`, `{"Scope":"CLOUDFRONT"}`}},
		{region: "us-west-2", want: []string{`{"Scope":"REGIONAL"}`}},
		{region: "eu-west-1", want: []string{`{"Scope":"REGIONAL"}`}},
		{region: "ap-southeast-2", want: []string{`{"Scope":"REGIONAL"}`}},
	}
	for _, tc := range cases {
		t.Run(tc.region, func(t *testing.T) {
			t.Parallel()
			got, err := wafv2ParentModels(context.Background(), aws.Config{}, tc.region, DiscoverArgs{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("region=%s scopes: got %v, want %v", tc.region, got, tc.want)
			}
		})
	}
}

// TestListCognitoUserPoolDomains_EmitsCompoundDomainAndCustomDomain
// pins the rare-state behavior where a single user pool has BOTH
// Domain (Cognito-hosted) and CustomDomain (customer DNS) configured.
// CFN treats those as two distinct AWS::Cognito::UserPoolDomain
// resources with separate primary identifiers, so the SDKLister must
// emit both.
//
// CRITICAL (#421): the emitted identifier MUST be the compound
// `<UserPoolId>|<Domain>` (or `<UserPoolId>|<CustomDomain>`), not the
// bare Domain string. AWS::Cognito::UserPoolDomain's CC primary
// identifier requires both properties (per CFN schema), and CC
// GetResource returns ValidationException on a bare-domain
// identifier. A regression that flips back to bare-domain would
// re-trigger the #412 live-smoke failure mode.
func TestListCognitoUserPoolDomains_EmitsCompoundDomainAndCustomDomain(t *testing.T) {
	t.Parallel()
	fake := &fakeCognitoUserPoolsLister{
		listPages: []cognitoidentityprovider.ListUserPoolsOutput{
			cognitoListPage("", "pool-with-domain", "pool-with-both", "pool-with-neither", "pool-with-custom-only"),
		},
		describeByID: map[string]cognitoidentityprovider.DescribeUserPoolOutput{
			"pool-with-domain": {UserPool: &cogniidptypes.UserPoolType{
				Domain: aws.String("auth.example"),
			}},
			"pool-with-both": {UserPool: &cogniidptypes.UserPoolType{
				Domain:       aws.String("co-hosted.example"),
				CustomDomain: aws.String("custom.example"),
			}},
			"pool-with-neither": {UserPool: &cogniidptypes.UserPoolType{}},
			"pool-with-custom-only": {UserPool: &cogniidptypes.UserPoolType{
				CustomDomain: aws.String("alone.example"),
			}},
		},
	}
	got, err := listCognitoUserPoolDomainsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Order is implementation-defined (pool walk × Domain-then-CustomDomain).
	// `want` is already sorted; sort `got` to compare set-shaped.
	sort.Strings(got)
	want := []string{
		"pool-with-both|co-hosted.example",
		"pool-with-both|custom.example",
		"pool-with-custom-only|alone.example",
		"pool-with-domain|auth.example",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("identifiers drift (must be <UserPoolId>|<Domain> compound per #421):\n got %v\nwant %v", got, want)
	}
	// Defensive: each emitted ID is the compound shape. A regression
	// that emits bare Domain would still produce 4 entries (and the
	// drift check above catches it), but a contains-`|` shape check
	// makes the failure message obvious — "missing pipe" is faster
	// for a future reader than diffing two 4-element slices.
	for _, id := range got {
		if !strings.Contains(id, "|") {
			t.Errorf("emitted identifier %q lacks `|` separator; #421 requires <UserPoolId>|<Domain> compound", id)
		}
	}
	// Every pool was probed exactly once (no DescribeUserPool retry
	// or skip — a regression that short-circuits on first-empty-pool
	// would survive only here).
	if fake.describeCalls != 4 {
		t.Errorf("describeCalls=%d, want 4 (one per pool)", fake.describeCalls)
	}
}

// TestListCognitoUserPoolDomains_PropagatesDescribeError pins that a
// DescribeUserPool failure for a single pool aborts enumeration via
// errors.Is — symmetric with the cloudControlDiscoverer's
// ListResources error path. Partial-success would silently emit a
// truncated set; explicit propagation lets callers retry.
func TestListCognitoUserPoolDomains_PropagatesDescribeError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: cognito-idp:DescribeUserPool")
	fake := &fakeCognitoUserPoolsLister{
		listPages: []cognitoidentityprovider.ListUserPoolsOutput{
			cognitoListPage("", "pool-a", "pool-b"),
		},
		describeByID: map[string]cognitoidentityprovider.DescribeUserPoolOutput{
			"pool-a": {UserPool: &cogniidptypes.UserPoolType{Domain: aws.String("a.example")}},
		},
		describeErr:    sentinel,
		describeErrFor: "pool-b",
	}
	got, err := listCognitoUserPoolDomainsWithClient(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
	// Pin the no-partial-success contract: a mid-walk error must
	// return nil, not a truncated slice containing pool-a's compound
	// ID. Callers retrying on error expect to retry the whole walk;
	// a partial slice combined with a retry would double-emit
	// pool-a|a.example.
	if got != nil {
		t.Errorf("partial result leaked on error: got %v, want nil", got)
	}
}

// fakeACMCertificatesLister is a hand-rolled fake for the ACM SDK
// subset used by listACMCertificates.
type fakeACMCertificatesLister struct {
	listPages []acm.ListCertificatesOutput
	listCalls int
	listErr   error
}

func (f *fakeACMCertificatesLister) ListCertificates(_ context.Context, _ *acm.ListCertificatesInput, _ ...func(*acm.Options)) (*acm.ListCertificatesOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &acm.ListCertificatesOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func acmListPage(token string, arns ...string) acm.ListCertificatesOutput {
	summaries := make([]acmtypes.CertificateSummary, 0, len(arns))
	for _, a := range arns {
		summaries = append(summaries, acmtypes.CertificateSummary{CertificateArn: aws.String(a)})
	}
	out := acm.ListCertificatesOutput{CertificateSummaryList: summaries}
	if token != "" {
		out.NextToken = aws.String(token)
	}
	return out
}

// TestListACMCertificates_PaginatesARNs pins that the ACM SDKLister
// paginates and emits one ARN per certificate. This is the canonical
// SDKLister-pattern test — a regression that drops pagination or emits
// the wrong field would silently truncate the cert set.
func TestListACMCertificates_PaginatesARNs(t *testing.T) {
	t.Parallel()
	fake := &fakeACMCertificatesLister{
		listPages: []acm.ListCertificatesOutput{
			acmListPage("tok1",
				"arn:aws:acm:us-east-1:111:certificate/aaa-111",
				"arn:aws:acm:us-east-1:111:certificate/bbb-222",
			),
			acmListPage("",
				"arn:aws:acm:us-east-1:111:certificate/ccc-333",
			),
		},
	}
	got, err := listACMCertificatesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"arn:aws:acm:us-east-1:111:certificate/aaa-111",
		"arn:aws:acm:us-east-1:111:certificate/bbb-222",
		"arn:aws:acm:us-east-1:111:certificate/ccc-333",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ARNs drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2 (paginated)", fake.listCalls)
	}
}

// TestListACMCertificates_EmptyReturnsNonNilEmpty pins the non-nil
// empty contract: zero certs returns []string{}, not nil. The
// discoverer's `len(ids) == 0` early-exit needs the slice to be
// non-nil for the empty-region branch to fire cleanly.
func TestListACMCertificates_EmptyReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeACMCertificatesLister{
		listPages: []acm.ListCertificatesOutput{
			acmListPage(""),
		},
	}
	got, err := listACMCertificatesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty-region result must be non-nil")
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

// fakeAPIGWv2APIsLister is a hand-rolled fake for the ApiGatewayV2 SDK
// subset used by listApigatewayv2Apis. Mirrors the fake-and-pagination
// shape of the other listers in this file. tokensSeen captures each
// in.NextToken value the lister sends so tests can pin that the
// pagination cursor is actually round-tripped between pages (not just
// "called 3 times").
type fakeAPIGWv2APIsLister struct {
	listPages   []apigatewayv2.GetApisOutput
	listCalls   int
	listErr     error
	tokensSeen  []*string
}

func (f *fakeAPIGWv2APIsLister) GetApis(_ context.Context, in *apigatewayv2.GetApisInput, _ ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error) {
	f.tokensSeen = append(f.tokensSeen, in.NextToken)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &apigatewayv2.GetApisOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func apigwv2APIsPage(token string, apiIDs ...string) apigatewayv2.GetApisOutput {
	items := make([]apigwv2types.Api, 0, len(apiIDs))
	for _, id := range apiIDs {
		items = append(items, apigwv2types.Api{ApiId: aws.String(id)})
	}
	out := apigatewayv2.GetApisOutput{Items: items}
	if token != "" {
		out.NextToken = aws.String(token)
	}
	return out
}

// TestListApigatewayv2Apis_PaginatesAndReturnsModels pins the
// API-enumeration helper used by aws_apigatewayv2_route /
// _integration / _authorizer's ParentLister: every API across every
// page must surface as a well-formed JSON ResourceModel string with
// ApiId set. A regression that drops a page or emits malformed JSON
// would silently produce zero child Route/Integration/Authorizer
// emissions for any HTTP API after the first page.
func TestListApigatewayv2Apis_PaginatesAndReturnsModels(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIsLister{
		listPages: []apigatewayv2.GetApisOutput{
			apigwv2APIsPage("tok1", "api-aaa", "api-bbb"),
			apigwv2APIsPage("tok2", "api-ccc"),
			apigwv2APIsPage("", "api-ddd", "api-eee"),
		},
	}
	got, err := listApigatewayv2ApisWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`{"ApiId":"api-aaa"}`,
		`{"ApiId":"api-bbb"}`,
		`{"ApiId":"api-ccc"}`,
		`{"ApiId":"api-ddd"}`,
		`{"ApiId":"api-eee"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 3 {
		t.Errorf("listCalls=%d, want 3 (paginated across three pages)", fake.listCalls)
	}
	// Pin the pagination cursor round-trip: the lister must feed
	// each page's NextToken into the next request. A regression that
	// passes `nil` on every call (e.g. dropping `nextToken =
	// page.NextToken`) would still produce 3 calls because the fake
	// serves pages by call-count — only this assertion catches it.
	if len(fake.tokensSeen) != 3 {
		t.Fatalf("tokensSeen len=%d, want 3", len(fake.tokensSeen))
	}
	if fake.tokensSeen[0] != nil {
		t.Errorf("tokensSeen[0]=%q, want nil (first request must not send a NextToken)",
			aws.ToString(fake.tokensSeen[0]))
	}
	if aws.ToString(fake.tokensSeen[1]) != "tok1" {
		t.Errorf("tokensSeen[1]=%q, want tok1 (must round-trip page-1's NextToken)",
			aws.ToString(fake.tokensSeen[1]))
	}
	if aws.ToString(fake.tokensSeen[2]) != "tok2" {
		t.Errorf("tokensSeen[2]=%q, want tok2 (must round-trip page-2's NextToken)",
			aws.ToString(fake.tokensSeen[2]))
	}
	// Each emitted model must round-trip as JSON with the ApiId key
	// present — a malformed string would crash the downstream CC
	// ListResources call.
	for _, m := range got {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(m), &parsed); err != nil {
			t.Errorf("model %q is not valid JSON: %v", m, err)
			continue
		}
		if parsed["ApiId"] == "" {
			t.Errorf("model %q missing ApiId key", m)
		}
	}
}

// TestListApigatewayv2Apis_EmptyAccountReturnsNonNilEmpty pins the
// non-nil empty contract: zero APIs returns []string{}, not nil. The
// discoverer's `len(parentModels) == 0` early-exit branch needs the
// slice to be non-nil for the empty-region path to fire cleanly.
func TestListApigatewayv2Apis_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIsLister{
		listPages: []apigatewayv2.GetApisOutput{
			apigwv2APIsPage(""),
		},
	}
	got, err := listApigatewayv2ApisWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty-region result must be non-nil (else len-check early-exit misfires)")
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

// TestListApigatewayv2Apis_PropagatesListError pins error propagation:
// an apigateway:GetApis failure must surface to the caller wrapped, not
// silently swallowed. The discoverer's outer error path emits a
// ServiceFinish + propagates — losing the error here would silently
// skip the type for the whole region.
func TestListApigatewayv2Apis_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("apigateway:GetApis boom")
	fake := &fakeAPIGWv2APIsLister{listErr: sentinel}
	_, err := listApigatewayv2ApisWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain: got %v, want chain containing %v", err, sentinel)
	}
}

// TestListApigatewayv2Apis_EmptyStringNextTokenTerminates pins the
// other half of the pagination terminator: the loop must also break
// when GetApis returns NextToken=&"" (an empty pointer, not nil) on
// the final page. The lister guards on both `nil` AND
// `aws.ToString(*token) == ""`; without the empty-string branch we'd
// loop forever passing an empty token to GetApis (which most likely
// returns an error or silently restarts pagination).
func TestListApigatewayv2Apis_EmptyStringNextTokenTerminates(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIsLister{
		listPages: []apigatewayv2.GetApisOutput{
			// Final page deliberately has NextToken=&"" rather than nil.
			{
				Items:     []apigwv2types.Api{{ApiId: aws.String("api-final")}},
				NextToken: aws.String(""),
			},
		},
	}
	got, err := listApigatewayv2ApisWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{`{"ApiId":"api-final"}`}) {
		t.Errorf("models drift: got %v, want [api-final only]", got)
	}
	if fake.listCalls != 1 {
		t.Errorf("listCalls=%d, want 1 (empty-string NextToken must terminate the loop, not trigger another GetApis)",
			fake.listCalls)
	}
}

// TestListApigatewayv2Apis_SkipsEmptyApiID guards a defensive branch:
// an Api whose ApiId is "" (defensively guarded in the lister) must be
// dropped, never emitted as `{"ApiId":""}`. The downstream CC
// ListResources call would reject the empty-ID parent model.
func TestListApigatewayv2Apis_SkipsEmptyApiID(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWv2APIsLister{
		listPages: []apigatewayv2.GetApisOutput{
			{Items: []apigwv2types.Api{
				{ApiId: aws.String("api-good")},
				{ApiId: nil},
				{ApiId: aws.String("")},
				{ApiId: aws.String("api-also-good")},
			}},
		},
	}
	got, err := listApigatewayv2ApisWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`{"ApiId":"api-good"}`,
		`{"ApiId":"api-also-good"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty-ID skip drift:\n got %v\nwant %v", got, want)
	}
}

// ===========================================================================
// API Gateway v1 (REST) APIs lister — #422
// ===========================================================================

// fakeAPIGWRestAPIsLister is a hand-rolled fake for the API Gateway v1
// SDK subset used by listApigatewayRestAPIs. The v1 service paginates
// via `Position` (not NextToken), so positionsSeen captures each
// in.Position the lister sends to pin the round-trip cursor.
type fakeAPIGWRestAPIsLister struct {
	listPages     []apigateway.GetRestApisOutput
	listCalls     int
	listErr       error
	positionsSeen []*string
}

func (f *fakeAPIGWRestAPIsLister) GetRestApis(_ context.Context, in *apigateway.GetRestApisInput, _ ...func(*apigateway.Options)) (*apigateway.GetRestApisOutput, error) {
	f.positionsSeen = append(f.positionsSeen, in.Position)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &apigateway.GetRestApisOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func apigwRestAPIsPage(position string, restAPIIds ...string) apigateway.GetRestApisOutput {
	items := make([]apigwtypes.RestApi, 0, len(restAPIIds))
	for _, id := range restAPIIds {
		items = append(items, apigwtypes.RestApi{Id: aws.String(id)})
	}
	out := apigateway.GetRestApisOutput{Items: items}
	if position != "" {
		out.Position = aws.String(position)
	}
	return out
}

// TestListApigatewayRestAPIs_PaginatesAndReturnsModels pins the
// API-enumeration helper shared by aws_api_gateway_{stage,deployment,resource}'s
// ParentLister: every REST API across every page must surface as a
// well-formed JSON ResourceModel string with RestApiId set. A regression
// that drops a page or emits malformed JSON would silently produce zero
// child Stage/Deployment/Resource emissions after the first page.
//
// Also pins the Position cursor round-trip (positionsSeen) — without
// this, a regression that drops `position = page.Position` would still
// produce 3 calls because the fake serves by call-count.
func TestListApigatewayRestAPIs_PaginatesAndReturnsModels(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWRestAPIsLister{
		listPages: []apigateway.GetRestApisOutput{
			apigwRestAPIsPage("pos1", "rest-aaa", "rest-bbb"),
			apigwRestAPIsPage("pos2", "rest-ccc"),
			apigwRestAPIsPage("", "rest-ddd", "rest-eee"),
		},
	}
	got, err := listApigatewayRestAPIsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`{"RestApiId":"rest-aaa"}`,
		`{"RestApiId":"rest-bbb"}`,
		`{"RestApiId":"rest-ccc"}`,
		`{"RestApiId":"rest-ddd"}`,
		`{"RestApiId":"rest-eee"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 3 {
		t.Errorf("listCalls=%d, want 3 (paginated across three pages)", fake.listCalls)
	}
	if len(fake.positionsSeen) != 3 {
		t.Fatalf("positionsSeen len=%d, want 3", len(fake.positionsSeen))
	}
	if fake.positionsSeen[0] != nil {
		t.Errorf("positionsSeen[0]=%q, want nil (first request must not send a Position)",
			aws.ToString(fake.positionsSeen[0]))
	}
	if aws.ToString(fake.positionsSeen[1]) != "pos1" {
		t.Errorf("positionsSeen[1]=%q, want pos1 (must round-trip page-1's Position)",
			aws.ToString(fake.positionsSeen[1]))
	}
	if aws.ToString(fake.positionsSeen[2]) != "pos2" {
		t.Errorf("positionsSeen[2]=%q, want pos2 (must round-trip page-2's Position)",
			aws.ToString(fake.positionsSeen[2]))
	}
	for _, m := range got {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(m), &parsed); err != nil {
			t.Errorf("model %q is not valid JSON: %v", m, err)
			continue
		}
		if parsed["RestApiId"] == "" {
			t.Errorf("model %q missing RestApiId key", m)
		}
	}
}

// TestListApigatewayRestAPIs_EmptyAccountReturnsNonNilEmpty pins the
// non-nil empty contract: zero REST APIs returns []string{}, not nil.
// The discoverer's `len(parentModels) == 0` early-exit branch needs the
// slice to be non-nil for the empty-region path to fire cleanly.
func TestListApigatewayRestAPIs_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWRestAPIsLister{
		listPages: []apigateway.GetRestApisOutput{
			apigwRestAPIsPage(""),
		},
	}
	got, err := listApigatewayRestAPIsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty-region result must be non-nil (else len-check early-exit misfires)")
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

// TestListApigatewayRestAPIs_PropagatesListError pins error
// propagation: an apigateway:GetRestApis failure must surface to the
// caller wrapped, not silently swallowed. The discoverer's outer error
// path emits a ServiceFinish + propagates — losing the error here
// would silently skip the type for the whole region.
func TestListApigatewayRestAPIs_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("apigateway:GetRestApis boom")
	fake := &fakeAPIGWRestAPIsLister{listErr: sentinel}
	_, err := listApigatewayRestAPIsWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain: got %v, want chain containing %v", err, sentinel)
	}
}

// TestListApigatewayRestAPIs_EmptyStringPositionTerminates pins the
// other half of the pagination terminator: the loop must also break
// when GetRestApis returns Position=&"" (an empty pointer, not nil) on
// the final page. The lister guards on both `nil` AND
// `aws.ToString(*position)==""`; without the empty-string branch we'd
// loop forever passing an empty Position to GetRestApis.
func TestListApigatewayRestAPIs_EmptyStringPositionTerminates(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWRestAPIsLister{
		listPages: []apigateway.GetRestApisOutput{
			// Final page deliberately has Position=&"" rather than nil.
			{
				Items:    []apigwtypes.RestApi{{Id: aws.String("rest-final")}},
				Position: aws.String(""),
			},
		},
	}
	got, err := listApigatewayRestAPIsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{`{"RestApiId":"rest-final"}`}) {
		t.Errorf("models drift: got %v, want [rest-final only]", got)
	}
	if fake.listCalls != 1 {
		t.Errorf("listCalls=%d, want 1 (empty-string Position must terminate the loop, not trigger another GetRestApis)",
			fake.listCalls)
	}
}

// TestListApigatewayRestAPIs_SkipsEmptyRestApiID guards the defensive
// branch: a RestApi whose Id is "" or nil must be dropped, never
// emitted as `{"RestApiId":""}`. The downstream CC ListResources call
// would reject the empty-ID parent model.
func TestListApigatewayRestAPIs_SkipsEmptyRestApiID(t *testing.T) {
	t.Parallel()
	fake := &fakeAPIGWRestAPIsLister{
		listPages: []apigateway.GetRestApisOutput{
			{Items: []apigwtypes.RestApi{
				{Id: aws.String("rest-good")},
				{Id: nil},
				{Id: aws.String("")},
				{Id: aws.String("rest-also-good")},
			}},
		},
	}
	got, err := listApigatewayRestAPIsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`{"RestApiId":"rest-good"}`,
		`{"RestApiId":"rest-also-good"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty-ID skip drift:\n got %v\nwant %v", got, want)
	}
}

// ===========================================================================
// Lambda function ARNs lister — #422 (parent for aws_lambda_function_url)
// ===========================================================================

// lambdaArnListPage builds a ListFunctionsOutput populated with both
// FunctionName and FunctionArn for each function — the
// listLambdaFunctionArns lister reads FunctionArn (not FunctionName).
// Without populating FunctionArn, the lister would emit empty-key
// models and the defensive skip would drop every function.
func lambdaArnListPage(marker string, fnArns ...string) lambda.ListFunctionsOutput {
	fns := make([]lambdatypes.FunctionConfiguration, 0, len(fnArns))
	for _, a := range fnArns {
		fns = append(fns, lambdatypes.FunctionConfiguration{FunctionArn: aws.String(a)})
	}
	out := lambda.ListFunctionsOutput{Functions: fns}
	if marker != "" {
		out.NextMarker = aws.String(marker)
	}
	return out
}

// TestListLambdaFunctionArns_PaginatesAndReturnsModels pins the
// ARN-keyed Lambda-functions enumerator used by aws_lambda_function_url's
// ParentLister: every function's ARN across every page surfaces as a
// JSON ResourceModel with TargetFunctionArn set (NOT FunctionName — the
// CC list-handler schema for AWS::Lambda::Url keys on TargetFunctionArn).
// A regression that flipped to FunctionName here would silently emit
// `{"TargetFunctionArn":""}` for every function (since fixtures populate
// only FunctionArn) and the defensive skip would yield zero parents.
func TestListLambdaFunctionArns_PaginatesAndReturnsModels(t *testing.T) {
	t.Parallel()
	fake := &fakeLambdaFunctionsLister{
		listPages: []lambda.ListFunctionsOutput{
			lambdaArnListPage("m1",
				"arn:aws:lambda:us-east-1:111:function:fn-alpha",
				"arn:aws:lambda:us-east-1:111:function:fn-beta",
			),
			lambdaArnListPage("",
				"arn:aws:lambda:us-east-1:111:function:fn-gamma",
			),
		},
	}
	got, err := listLambdaFunctionArnsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`{"TargetFunctionArn":"arn:aws:lambda:us-east-1:111:function:fn-alpha"}`,
		`{"TargetFunctionArn":"arn:aws:lambda:us-east-1:111:function:fn-beta"}`,
		`{"TargetFunctionArn":"arn:aws:lambda:us-east-1:111:function:fn-gamma"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("models drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2", fake.listCalls)
	}
	// Each emitted model must round-trip as JSON with TargetFunctionArn
	// set — a malformed string would crash the downstream CC
	// ListResources call.
	for _, m := range got {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(m), &parsed); err != nil {
			t.Errorf("model %q is not valid JSON: %v", m, err)
			continue
		}
		if parsed["TargetFunctionArn"] == "" {
			t.Errorf("model %q missing TargetFunctionArn key", m)
		}
	}
}

// TestListLambdaFunctionArns_EmptyAccountReturnsNonNilEmpty pins the
// non-nil empty contract for the ARN-keyed lister: zero functions
// returns []string{}, not nil.
func TestListLambdaFunctionArns_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeLambdaFunctionsLister{
		listPages: []lambda.ListFunctionsOutput{lambdaArnListPage("")},
	}
	got, err := listLambdaFunctionArnsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("empty-region result must be non-nil")
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

// TestListLambdaFunctionArns_PropagatesListError pins error
// propagation: a lambda:ListFunctions failure surfaces wrapped to
// the caller via errors.Is.
func TestListLambdaFunctionArns_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("lambda:ListFunctions boom")
	fake := &fakeLambdaFunctionsLister{listErr: sentinel}
	_, err := listLambdaFunctionArnsWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain: got %v, want chain containing %v", err, sentinel)
	}
}

// TestListLambdaFunctionArns_SkipsEmptyFunctionArn pins the defensive
// skip: a function with FunctionArn nil or "" must be dropped rather
// than emitted as `{"TargetFunctionArn":""}`. A regression that flipped
// the field read from FunctionArn to FunctionName would also be caught
// here (fixtures populate only FunctionArn, so the wrong-field read
// would yield three empty-ARN drops).
func TestListLambdaFunctionArns_SkipsEmptyFunctionArn(t *testing.T) {
	t.Parallel()
	fake := &fakeLambdaFunctionsLister{
		listPages: []lambda.ListFunctionsOutput{
			{Functions: []lambdatypes.FunctionConfiguration{
				{FunctionArn: aws.String("arn:aws:lambda:us-east-1:111:function:fn-good")},
				{FunctionArn: nil},
				{FunctionArn: aws.String("")},
				{FunctionArn: aws.String("arn:aws:lambda:us-east-1:111:function:fn-also-good")},
			}},
		},
	}
	got, err := listLambdaFunctionArnsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`{"TargetFunctionArn":"arn:aws:lambda:us-east-1:111:function:fn-good"}`,
		`{"TargetFunctionArn":"arn:aws:lambda:us-east-1:111:function:fn-also-good"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty-ARN skip drift:\n got %v\nwant %v", got, want)
	}
}
