package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
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

// TestListCognitoUserPoolDomains_EmitsDomainAndCustomDomain pins the
// rare-state behavior where a single user pool has BOTH Domain
// (Cognito-hosted) and CustomDomain (customer DNS) configured. CFN
// treats those as two distinct AWS::Cognito::UserPoolDomain resources
// with separate primary identifiers, so the SDKLister must emit both.
func TestListCognitoUserPoolDomains_EmitsDomainAndCustomDomain(t *testing.T) {
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
	// Order is implementation-defined (pool walk × Domain-then-CustomDomain);
	// sort both sides for comparison.
	sort.Strings(got)
	want := []string{"alone.example", "auth.example", "co-hosted.example", "custom.example"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("domains drift:\n got %v\nwant %v", got, want)
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
	_, err := listCognitoUserPoolDomainsWithClient(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err does not wrap sentinel; got %v", err)
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
