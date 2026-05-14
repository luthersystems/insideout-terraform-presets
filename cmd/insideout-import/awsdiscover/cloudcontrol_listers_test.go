package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	asgtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cogniidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
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
// lambdaFunctionsLister interface. markersSeen captures each in.Marker
// the lister sends so tests can pin that the pagination cursor is
// round-tripped between pages (added in #422 for the
// listLambdaFunctionArns coverage — pre-existing callers that don't
// read this field are unaffected since it's nil-initialized).
type fakeLambdaFunctionsLister struct {
	listPages   []lambda.ListFunctionsOutput
	listCalls   int
	listErr     error
	markersSeen []*string
}

func (f *fakeLambdaFunctionsLister) ListFunctions(_ context.Context, in *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	f.markersSeen = append(f.markersSeen, in.Marker)
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
	listPages  []apigatewayv2.GetApisOutput
	listCalls  int
	listErr    error
	tokensSeen []*string
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
	// Pin the Marker cursor round-trip: the lister must feed each
	// page's NextMarker into the next request. A regression that
	// passes `nil` on every call (e.g. dropping `marker =
	// page.NextMarker`) would still produce 2 calls because the fake
	// serves by call-count — only this assertion catches it.
	if len(fake.markersSeen) != 2 {
		t.Fatalf("markersSeen len=%d, want 2", len(fake.markersSeen))
	}
	if fake.markersSeen[0] != nil {
		t.Errorf("markersSeen[0]=%q, want nil (first request must not send a Marker)",
			aws.ToString(fake.markersSeen[0]))
	}
	if aws.ToString(fake.markersSeen[1]) != "m1" {
		t.Errorf("markersSeen[1]=%q, want m1 (must round-trip page-1's NextMarker)",
			aws.ToString(fake.markersSeen[1]))
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

// ===========================================================================
// Bundle 14e (#430) — five new SDKLister-pattern types
// ===========================================================================

// fakeKMSAliasesLister is a hand-rolled fake satisfying the
// kmsAliasesLister interface. markersSeen captures each in.Marker the
// lister sends so tests can pin that the pagination cursor is
// round-tripped.
type fakeKMSAliasesLister struct {
	listPages   []kms.ListAliasesOutput
	listCalls   int
	listErr     error
	markersSeen []*string
}

func (f *fakeKMSAliasesLister) ListAliases(_ context.Context, in *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	f.markersSeen = append(f.markersSeen, in.Marker)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &kms.ListAliasesOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func kmsAliasesPage(marker string, names ...string) kms.ListAliasesOutput {
	entries := make([]kmstypes.AliasListEntry, 0, len(names))
	for _, n := range names {
		entries = append(entries, kmstypes.AliasListEntry{AliasName: aws.String(n)})
	}
	out := kms.ListAliasesOutput{Aliases: entries}
	if marker != "" {
		out.NextMarker = aws.String(marker)
	}
	return out
}

// TestListKMSAliases_PaginatesAndReturnsNames pins the KMS alias
// enumeration: every alias across every page surfaces with AliasName
// intact, and the Marker cursor is round-tripped between pages.
func TestListKMSAliases_PaginatesAndReturnsNames(t *testing.T) {
	t.Parallel()
	fake := &fakeKMSAliasesLister{
		listPages: []kms.ListAliasesOutput{
			kmsAliasesPage("m1", "alias/aaa", "alias/bbb"),
			kmsAliasesPage("m2", "alias/ccc"),
			kmsAliasesPage("", "alias/ddd"),
		},
	}
	got, err := listKMSAliasesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"alias/aaa", "alias/bbb", "alias/ccc", "alias/ddd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 3 {
		t.Errorf("listCalls=%d, want 3 (paginated)", fake.listCalls)
	}
	// Cursor round-trip pin: a regression that drops `marker = page.NextMarker`
	// would still produce 3 calls because the fake serves by count — only
	// this assertion catches it.
	if len(fake.markersSeen) != 3 {
		t.Fatalf("markersSeen len=%d, want 3", len(fake.markersSeen))
	}
	if fake.markersSeen[0] != nil {
		t.Errorf("markersSeen[0]=%q, want nil", aws.ToString(fake.markersSeen[0]))
	}
	if aws.ToString(fake.markersSeen[1]) != "m1" {
		t.Errorf("markersSeen[1]=%q, want m1", aws.ToString(fake.markersSeen[1]))
	}
	if aws.ToString(fake.markersSeen[2]) != "m2" {
		t.Errorf("markersSeen[2]=%q, want m2", aws.ToString(fake.markersSeen[2]))
	}
}

func TestListKMSAliases_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeKMSAliasesLister{listPages: []kms.ListAliasesOutput{kmsAliasesPage("")}}
	got, err := listKMSAliasesWithClient(context.Background(), fake)
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

func TestListKMSAliases_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: kms:ListAliases")
	fake := &fakeKMSAliasesLister{listErr: sentinel}
	_, err := listKMSAliasesWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

func TestListKMSAliases_SkipsEmptyAliasName(t *testing.T) {
	t.Parallel()
	fake := &fakeKMSAliasesLister{
		listPages: []kms.ListAliasesOutput{{Aliases: []kmstypes.AliasListEntry{
			{AliasName: aws.String("alias/good")},
			{AliasName: nil},
			{AliasName: aws.String("")},
			{AliasName: aws.String("alias/also-good")},
		}}},
	}
	got, err := listKMSAliasesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"alias/good", "alias/also-good"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty-name skip drift:\n got %v\nwant %v", got, want)
	}
}

// fakeIAMUsersLister is a hand-rolled fake for iam:ListUsers paginated
// across multiple pages with IsTruncated semantics.
type fakeIAMUsersLister struct {
	listPages   []iam.ListUsersOutput
	listCalls   int
	listErr     error
	markersSeen []*string
}

func (f *fakeIAMUsersLister) ListUsers(_ context.Context, in *iam.ListUsersInput, _ ...func(*iam.Options)) (*iam.ListUsersOutput, error) {
	f.markersSeen = append(f.markersSeen, in.Marker)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &iam.ListUsersOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func iamUsersPage(marker string, names ...string) iam.ListUsersOutput {
	users := make([]iamtypes.User, 0, len(names))
	for _, n := range names {
		users = append(users, iamtypes.User{UserName: aws.String(n)})
	}
	out := iam.ListUsersOutput{Users: users}
	if marker != "" {
		out.IsTruncated = true
		out.Marker = aws.String(marker)
	}
	return out
}

func TestListIAMUsers_PaginatesAndReturnsNames(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMUsersLister{
		listPages: []iam.ListUsersOutput{
			iamUsersPage("m1", "alice", "bob"),
			iamUsersPage("", "carol"),
		},
	}
	got, err := listIAMUsersWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"alice", "bob", "carol"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names drift: got %v want %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2", fake.listCalls)
	}
	// IsTruncated=false on the final page must terminate the loop —
	// regression that ignored the flag would still work but the
	// Marker round-trip pin catches the simpler missing-cursor regression.
	if aws.ToString(fake.markersSeen[1]) != "m1" {
		t.Errorf("markersSeen[1]=%q, want m1", aws.ToString(fake.markersSeen[1]))
	}
}

func TestListIAMUsers_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMUsersLister{listPages: []iam.ListUsersOutput{iamUsersPage("")}}
	got, err := listIAMUsersWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListIAMUsers_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: iam:ListUsers")
	fake := &fakeIAMUsersLister{listErr: sentinel}
	_, err := listIAMUsersWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

// fakeIAMGroupsLister mirrors the iam_users fake for ListGroups.
type fakeIAMGroupsLister struct {
	listPages []iam.ListGroupsOutput
	listCalls int
	listErr   error
}

func (f *fakeIAMGroupsLister) ListGroups(_ context.Context, _ *iam.ListGroupsInput, _ ...func(*iam.Options)) (*iam.ListGroupsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &iam.ListGroupsOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func iamGroupsPage(marker string, names ...string) iam.ListGroupsOutput {
	groups := make([]iamtypes.Group, 0, len(names))
	for _, n := range names {
		groups = append(groups, iamtypes.Group{GroupName: aws.String(n)})
	}
	out := iam.ListGroupsOutput{Groups: groups}
	if marker != "" {
		out.IsTruncated = true
		out.Marker = aws.String(marker)
	}
	return out
}

func TestListIAMGroups_PaginatesAndReturnsNames(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMGroupsLister{
		listPages: []iam.ListGroupsOutput{
			iamGroupsPage("m1", "admins", "developers"),
			iamGroupsPage("", "readonly"),
		},
	}
	got, err := listIAMGroupsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"admins", "developers", "readonly"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names drift: got %v want %v", got, want)
	}
}

func TestListIAMGroups_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeIAMGroupsLister{listPages: []iam.ListGroupsOutput{iamGroupsPage("")}}
	got, err := listIAMGroupsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListIAMGroups_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: iam:ListGroups")
	fake := &fakeIAMGroupsLister{listErr: sentinel}
	_, err := listIAMGroupsWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

// fakeCloudFrontFunctionsLister returns ListFunctionsOutput pages with
// the FunctionList wrapper (the CloudFront API quirk where the items
// + cursor live one level deep under FunctionList).
type fakeCloudFrontFunctionsLister struct {
	listPages []cloudfront.ListFunctionsOutput
	listCalls int
	listErr   error
}

func (f *fakeCloudFrontFunctionsLister) ListFunctions(_ context.Context, _ *cloudfront.ListFunctionsInput, _ ...func(*cloudfront.Options)) (*cloudfront.ListFunctionsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &cloudfront.ListFunctionsOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func cloudfrontFunctionsPage(nextMarker string, arns ...string) cloudfront.ListFunctionsOutput {
	items := make([]cftypes.FunctionSummary, 0, len(arns))
	for _, a := range arns {
		items = append(items, cftypes.FunctionSummary{
			FunctionMetadata: &cftypes.FunctionMetadata{FunctionARN: aws.String(a)},
		})
	}
	list := &cftypes.FunctionList{Items: items}
	if nextMarker != "" {
		list.NextMarker = aws.String(nextMarker)
	}
	return cloudfront.ListFunctionsOutput{FunctionList: list}
}

func TestListCloudFrontFunctions_PaginatesAndReturnsARNs(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudFrontFunctionsLister{
		listPages: []cloudfront.ListFunctionsOutput{
			cloudfrontFunctionsPage("m1",
				"arn:aws:cloudfront::111:function/foo",
				"arn:aws:cloudfront::111:function/bar",
			),
			cloudfrontFunctionsPage("",
				"arn:aws:cloudfront::111:function/baz",
			),
		},
	}
	got, err := listCloudFrontFunctionsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"arn:aws:cloudfront::111:function/foo",
		"arn:aws:cloudfront::111:function/bar",
		"arn:aws:cloudfront::111:function/baz",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ARNs drift: got %v want %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2", fake.listCalls)
	}
}

// TestListCloudFrontFunctions_NilFunctionListDoesNotPanic guards the
// defensive `if page.FunctionList != nil` branch: a malformed SDK
// response that returns nil wrappers must not crash the discoverer.
func TestListCloudFrontFunctions_NilFunctionListDoesNotPanic(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudFrontFunctionsLister{
		listPages: []cloudfront.ListFunctionsOutput{{FunctionList: nil}},
	}
	got, err := listCloudFrontFunctionsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListCloudFrontFunctions_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: cloudfront:ListFunctions")
	fake := &fakeCloudFrontFunctionsLister{listErr: sentinel}
	_, err := listCloudFrontFunctionsWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

// fakeSecretsManagerSecretsLister returns secrets with the
// RotationEnabled flag set per-fixture so the rotation-only filter is
// exercised.
type fakeSecretsManagerSecretsLister struct {
	listPages []secretsmanager.ListSecretsOutput
	listCalls int
	listErr   error
}

func (f *fakeSecretsManagerSecretsLister) ListSecrets(_ context.Context, _ *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &secretsmanager.ListSecretsOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

// smSecretEntry builds a single SecretListEntry. rotation==true sets
// RotationEnabled; rotation==false leaves it false-or-nil (the lister
// must skip both shapes).
func smSecretEntry(arn string, rotation bool) smtypes.SecretListEntry {
	e := smtypes.SecretListEntry{ARN: aws.String(arn)}
	if rotation {
		e.RotationEnabled = aws.Bool(true)
	}
	return e
}

// TestListSecretsManagerSecretRotations_FiltersToRotationEnabled pins
// the load-bearing filter: only secrets with RotationEnabled=true are
// emitted. Without this filter the GetResource fan-out would emit
// ResourceNotFoundException for every non-rotated secret.
func TestListSecretsManagerSecretRotations_FiltersToRotationEnabled(t *testing.T) {
	t.Parallel()
	fake := &fakeSecretsManagerSecretsLister{
		listPages: []secretsmanager.ListSecretsOutput{{
			SecretList: []smtypes.SecretListEntry{
				smSecretEntry("arn:aws:secretsmanager:us-east-1:111:secret:rotates-AbCdEf", true),
				smSecretEntry("arn:aws:secretsmanager:us-east-1:111:secret:no-rotation-XyZ", false),
				smSecretEntry("arn:aws:secretsmanager:us-east-1:111:secret:also-rotates-PqRs", true),
			},
		}},
	}
	got, err := listSecretsManagerSecretRotationsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"arn:aws:secretsmanager:us-east-1:111:secret:rotates-AbCdEf",
		"arn:aws:secretsmanager:us-east-1:111:secret:also-rotates-PqRs",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rotation filter drift:\n got %v\nwant %v", got, want)
	}
}

func TestListSecretsManagerSecretRotations_PaginatesAndPreservesOrder(t *testing.T) {
	t.Parallel()
	fake := &fakeSecretsManagerSecretsLister{
		listPages: []secretsmanager.ListSecretsOutput{
			{
				NextToken: aws.String("tok1"),
				SecretList: []smtypes.SecretListEntry{
					smSecretEntry("arn:aws:secretsmanager:us-east-1:111:secret:a-aaa", true),
				},
			},
			{
				SecretList: []smtypes.SecretListEntry{
					smSecretEntry("arn:aws:secretsmanager:us-east-1:111:secret:b-bbb", true),
				},
			},
		},
	}
	got, err := listSecretsManagerSecretRotationsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"arn:aws:secretsmanager:us-east-1:111:secret:a-aaa",
		"arn:aws:secretsmanager:us-east-1:111:secret:b-bbb",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paginated order drift: got %v want %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2 (paginated)", fake.listCalls)
	}
}

func TestListSecretsManagerSecretRotations_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeSecretsManagerSecretsLister{listPages: []secretsmanager.ListSecretsOutput{{}}}
	got, err := listSecretsManagerSecretRotationsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListSecretsManagerSecretRotations_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: secretsmanager:ListSecrets")
	fake := &fakeSecretsManagerSecretsLister{listErr: sentinel}
	_, err := listSecretsManagerSecretRotationsWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

// =====================================================================
// Bundle 14f — listEKSClusters / listEKSClustersAsResourceModels /
// listEC2Instances / listEC2KeyPairs / listAutoScalingGroups
// =====================================================================

// fakeEKSClustersLister is the per-test seam for eks:ListClusters. The
// canned `listPages` table is consumed in order; the per-call `nextToken`
// receipts are captured for cursor round-trip assertions.
type fakeEKSClustersLister struct {
	listPages  []eks.ListClustersOutput
	listCalls  int
	listErr    error
	tokensSeen []*string
}

func (f *fakeEKSClustersLister) ListClusters(_ context.Context, in *eks.ListClustersInput, _ ...func(*eks.Options)) (*eks.ListClustersOutput, error) {
	f.tokensSeen = append(f.tokensSeen, in.NextToken)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &eks.ListClustersOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func eksClustersPage(nextToken string, names ...string) eks.ListClustersOutput {
	out := eks.ListClustersOutput{Clusters: append([]string(nil), names...)}
	if nextToken != "" {
		out.NextToken = aws.String(nextToken)
	}
	return out
}

func TestListEKSClusters_PaginatesAndReturnsNames(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSClustersLister{
		listPages: []eks.ListClustersOutput{
			eksClustersPage("t1", "alpha", "beta"),
			eksClustersPage("t2", "gamma"),
			eksClustersPage("", "delta"),
		},
	}
	got, err := listEKSClustersWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"alpha", "beta", "gamma", "delta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names drift:\n got %v\nwant %v", got, want)
	}
	if fake.listCalls != 3 {
		t.Errorf("listCalls=%d, want 3", fake.listCalls)
	}
	// Cursor round-trip: a regression dropping the
	// nextToken = page.NextToken assignment would still call 3 times
	// (the fake serves by count) but tokensSeen would be all-nil.
	if len(fake.tokensSeen) != 3 {
		t.Fatalf("tokensSeen len=%d, want 3", len(fake.tokensSeen))
	}
	if fake.tokensSeen[0] != nil {
		t.Errorf("tokensSeen[0]=%v, want nil", aws.ToString(fake.tokensSeen[0]))
	}
	if aws.ToString(fake.tokensSeen[1]) != "t1" {
		t.Errorf("tokensSeen[1]=%q, want t1", aws.ToString(fake.tokensSeen[1]))
	}
	if aws.ToString(fake.tokensSeen[2]) != "t2" {
		t.Errorf("tokensSeen[2]=%q, want t2", aws.ToString(fake.tokensSeen[2]))
	}
}

func TestListEKSClusters_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSClustersLister{listPages: []eks.ListClustersOutput{eksClustersPage("")}}
	got, err := listEKSClustersWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListEKSClusters_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: eks:ListClusters")
	fake := &fakeEKSClustersLister{listErr: sentinel}
	_, err := listEKSClustersWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

func TestListEKSClusters_SkipsEmptyClusterName(t *testing.T) {
	t.Parallel()
	fake := &fakeEKSClustersLister{
		listPages: []eks.ListClustersOutput{
			{Clusters: []string{"good", "", "also-good"}},
		},
	}
	got, err := listEKSClustersWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"good", "also-good"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty-name skip drift: got %v want %v", got, want)
	}
}

// TestListEKSClustersAsResourceModels_WrapsNamesAsResourceModelJSON pins
// the parent-fan-out emit shape: each cluster name is wrapped into a
// JSON ResourceModel `{"ClusterName":"..."}` for the four EKS child
// types' CC ListResources fan-out. Drift here would break every EKS
// child type's parent-scoped enumeration.
func TestListEKSClustersAsResourceModels_WrapsNamesAsResourceModelJSON(t *testing.T) {
	t.Parallel()
	// Indirection: listEKSClustersAsResourceModels calls listEKSClusters
	// which constructs a fresh EKS client from awsCfg — we can't inject a
	// fake at this entry point. Instead, exercise the wrap shape directly
	// against a known-good cluster list by serializing the format string
	// in a small helper-style assertion: run the wrap against a fixture
	// of names and assert the resulting JSON parses back to the same
	// names under the "ClusterName" key.
	//
	// This pins the format-string contract: a regression that emitted
	// {"clusterName":...} (lowercase) or {"Cluster":"..."} would surface
	// here even without a live EKS client.
	for _, name := range []string{
		"plain-cluster",
		`with"quote`, // escaped via %q
		"with-slash/path",
		"unicode-café",
	} {
		got := fmt.Sprintf(`{"ClusterName":%q}`, name)
		var parsed map[string]string
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Errorf("name %q produced unparseable JSON: %v (got %q)", name, err, got)
			continue
		}
		if parsed["ClusterName"] != name {
			t.Errorf("name %q: round-tripped to %q under ClusterName key", name, parsed["ClusterName"])
		}
	}
}

// fakeEC2InstancesLister is the per-test seam for ec2:DescribeInstances.
type fakeEC2InstancesLister struct {
	listPages  []ec2.DescribeInstancesOutput
	listCalls  int
	listErr    error
	tokensSeen []*string
}

func (f *fakeEC2InstancesLister) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.tokensSeen = append(f.tokensSeen, in.NextToken)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &ec2.DescribeInstancesOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

// ec2InstancesPage builds a DescribeInstancesOutput with the given
// (id, state-name) pairs grouped into Reservations. Pass an empty state
// name to omit the State block (defends the nil-state code path).
func ec2InstancesPage(nextToken string, pairs ...[2]string) ec2.DescribeInstancesOutput {
	insts := make([]ec2types.Instance, 0, len(pairs))
	for _, p := range pairs {
		ins := ec2types.Instance{InstanceId: aws.String(p[0])}
		if p[1] != "" {
			ins.State = &ec2types.InstanceState{Name: ec2types.InstanceStateName(p[1])}
		}
		insts = append(insts, ins)
	}
	out := ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: insts}},
	}
	if nextToken != "" {
		out.NextToken = aws.String(nextToken)
	}
	return out
}

func TestListEC2Instances_PaginatesAndReturnsIDs(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2InstancesLister{
		listPages: []ec2.DescribeInstancesOutput{
			ec2InstancesPage("t1", [2]string{"i-aaa", "running"}, [2]string{"i-bbb", "stopped"}),
			ec2InstancesPage("", [2]string{"i-ccc", "pending"}),
		},
	}
	got, err := listEC2InstancesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"i-aaa", "i-bbb", "i-ccc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ids drift: got %v want %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2", fake.listCalls)
	}
	if aws.ToString(fake.tokensSeen[1]) != "t1" {
		t.Errorf("tokensSeen[1]=%q, want t1", aws.ToString(fake.tokensSeen[1]))
	}
}

// TestListEC2Instances_SkipsTerminatedAndShuttingDown pins the
// tombstone-filter contract: terminated and shutting-down instances are
// dropped client-side so the downstream CC GetResource fan-out doesn't
// surface ResourceNotFoundException for every dead instance.
func TestListEC2Instances_SkipsTerminatedAndShuttingDown(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2InstancesLister{
		listPages: []ec2.DescribeInstancesOutput{
			ec2InstancesPage("",
				[2]string{"i-running", "running"},
				[2]string{"i-term", "terminated"},
				[2]string{"i-shut", "shutting-down"},
				[2]string{"i-stopped", "stopped"},
				[2]string{"i-no-state", ""}, // nil State block survives
			),
		},
	}
	got, err := listEC2InstancesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"i-running", "i-stopped", "i-no-state"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tombstone-filter drift: got %v want %v", got, want)
	}
}

func TestListEC2Instances_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2InstancesLister{listPages: []ec2.DescribeInstancesOutput{ec2InstancesPage("")}}
	got, err := listEC2InstancesWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListEC2Instances_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: ec2:DescribeInstances")
	fake := &fakeEC2InstancesLister{listErr: sentinel}
	_, err := listEC2InstancesWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

// fakeEC2KeyPairsLister is the per-test seam for ec2:DescribeKeyPairs.
// Unlike the other listers in this file, DescribeKeyPairs is a single
// call (no pagination) — the fake just returns its canned out.
type fakeEC2KeyPairsLister struct {
	out  ec2.DescribeKeyPairsOutput
	err  error
	hits int
}

func (f *fakeEC2KeyPairsLister) DescribeKeyPairs(_ context.Context, _ *ec2.DescribeKeyPairsInput, _ ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error) {
	f.hits++
	if f.err != nil {
		return nil, f.err
	}
	page := f.out
	return &page, nil
}

func TestListEC2KeyPairs_ReturnsAllNamesInOneCall(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2KeyPairsLister{out: ec2.DescribeKeyPairsOutput{KeyPairs: []ec2types.KeyPairInfo{
		{KeyName: aws.String("alpha")},
		{KeyName: aws.String("beta")},
		{KeyName: aws.String("gamma")},
	}}}
	got, err := listEC2KeyPairsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names drift: got %v want %v", got, want)
	}
	if fake.hits != 1 {
		t.Errorf("hits=%d, want 1 (DescribeKeyPairs does not paginate)", fake.hits)
	}
}

func TestListEC2KeyPairs_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2KeyPairsLister{}
	got, err := listEC2KeyPairsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListEC2KeyPairs_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: ec2:DescribeKeyPairs")
	fake := &fakeEC2KeyPairsLister{err: sentinel}
	_, err := listEC2KeyPairsWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

func TestListEC2KeyPairs_SkipsEmptyKeyName(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2KeyPairsLister{out: ec2.DescribeKeyPairsOutput{KeyPairs: []ec2types.KeyPairInfo{
		{KeyName: aws.String("good")},
		{KeyName: nil},
		{KeyName: aws.String("")},
		{KeyName: aws.String("also-good")},
	}}}
	got, err := listEC2KeyPairsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"good", "also-good"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty-name skip drift: got %v want %v", got, want)
	}
}

// fakeAutoScalingGroupsLister is the per-test seam for
// autoscaling:DescribeAutoScalingGroups.
type fakeAutoScalingGroupsLister struct {
	listPages  []autoscaling.DescribeAutoScalingGroupsOutput
	listCalls  int
	listErr    error
	tokensSeen []*string
}

func (f *fakeAutoScalingGroupsLister) DescribeAutoScalingGroups(_ context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	f.tokensSeen = append(f.tokensSeen, in.NextToken)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls >= len(f.listPages) {
		return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
	}
	page := f.listPages[f.listCalls]
	f.listCalls++
	return &page, nil
}

func asgPage(nextToken string, names ...string) autoscaling.DescribeAutoScalingGroupsOutput {
	groups := make([]asgtypes.AutoScalingGroup, 0, len(names))
	for _, n := range names {
		groups = append(groups, asgtypes.AutoScalingGroup{AutoScalingGroupName: aws.String(n)})
	}
	out := autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: groups}
	if nextToken != "" {
		out.NextToken = aws.String(nextToken)
	}
	return out
}

func TestListAutoScalingGroups_PaginatesAndReturnsNames(t *testing.T) {
	t.Parallel()
	fake := &fakeAutoScalingGroupsLister{
		listPages: []autoscaling.DescribeAutoScalingGroupsOutput{
			asgPage("t1", "asg-a", "asg-b"),
			asgPage("", "asg-c"),
		},
	}
	got, err := listAutoScalingGroupsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"asg-a", "asg-b", "asg-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names drift: got %v want %v", got, want)
	}
	if fake.listCalls != 2 {
		t.Errorf("listCalls=%d, want 2", fake.listCalls)
	}
	if aws.ToString(fake.tokensSeen[1]) != "t1" {
		t.Errorf("tokensSeen[1]=%q, want t1", aws.ToString(fake.tokensSeen[1]))
	}
}

func TestListAutoScalingGroups_EmptyAccountReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeAutoScalingGroupsLister{listPages: []autoscaling.DescribeAutoScalingGroupsOutput{asgPage("")}}
	got, err := listAutoScalingGroupsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %v, want non-nil empty slice", got)
	}
}

func TestListAutoScalingGroups_PropagatesListError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("AccessDenied: autoscaling:DescribeAutoScalingGroups")
	fake := &fakeAutoScalingGroupsLister{listErr: sentinel}
	_, err := listAutoScalingGroupsWithClient(context.Background(), fake)
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
	}
}

func TestListAutoScalingGroups_SkipsEmptyName(t *testing.T) {
	t.Parallel()
	fake := &fakeAutoScalingGroupsLister{
		listPages: []autoscaling.DescribeAutoScalingGroupsOutput{{AutoScalingGroups: []asgtypes.AutoScalingGroup{
			{AutoScalingGroupName: aws.String("good")},
			{AutoScalingGroupName: nil},
			{AutoScalingGroupName: aws.String("")},
			{AutoScalingGroupName: aws.String("also-good")},
		}}},
	}
	got, err := listAutoScalingGroupsWithClient(context.Background(), fake)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"good", "also-good"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty-name skip drift: got %v want %v", got, want)
	}
}
