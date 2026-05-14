package gcpdiscover

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cloud.google.com/go/asset/apiv1/assetpb"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestAssetResultFromProto_FieldMapping pins the proto→gcpAssetResult
// mapping field-by-field. The per-type discoverers all start from a
// hand-built gcpAssetResult, so a mutation here (e.g. swapping
// `Name: r.GetName()` ← → `AssetType: r.GetAssetType()`, or dropping
// Location) would silently invalidate every downstream contract. Using
// distinct, non-overlapping values for every field guarantees a swap of
// any two is caught.
func TestAssetResultFromProto_FieldMapping(t *testing.T) {
	t.Parallel()
	in := &assetpb.ResourceSearchResult{
		Name:      "//pubsub.googleapis.com/projects/real-proj/topics/io-events",
		AssetType: "pubsub.googleapis.com/Topic",
		Project:   "real-proj",
		Location:  "us-central1",
		Labels:    map[string]string{"project": "io-foo", "owner": "team-a"},
	}
	got := assetResultFromProto(in)
	want := gcpAssetResult{
		Name:      "//pubsub.googleapis.com/projects/real-proj/topics/io-events",
		AssetType: "pubsub.googleapis.com/Topic",
		Project:   "real-proj",
		Location:  "us-central1",
		Labels:    map[string]string{"project": "io-foo", "owner": "team-a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assetResultFromProto mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// TestAssetResultFromProto_ZeroValueProtoYieldsZeroFields pins the
// GetX accessor idioms against a zero-valued (non-nil) proto. Every
// proto getter returns the zero value of its field type, so the
// flattened gcpAssetResult is zero across the board. A future
// refactor that replaced `r.GetName()` with `r.Name` would still pass
// this test (both return "" on a zero-valued proto); the nil-receiver
// case is covered by TestAssetResultFromProto_NilProtoIsSafe below.
func TestAssetResultFromProto_ZeroValueProtoYieldsZeroFields(t *testing.T) {
	t.Parallel()
	got := assetResultFromProto(&assetpb.ResourceSearchResult{})
	if got.Name != "" || got.AssetType != "" || got.Project != "" || got.Location != "" || got.Labels != nil {
		t.Errorf("zero-proto must produce zero result; got %+v", got)
	}
}

// TestAssetResultFromProto_NilProtoIsSafe pins the contract that
// protoc-generated GetX accessors handle a nil receiver — a refactor
// from `r.GetName()` to `r.Name` would panic here and break dep-chase
// any time SearchAllResources surfaced a nil row (rare but possible
// on transient gRPC errors that the iterator surfaces alongside the
// stream).
func TestAssetResultFromProto_NilProtoIsSafe(t *testing.T) {
	t.Parallel()
	got := assetResultFromProto(nil)
	if got.Name != "" || got.AssetType != "" || got.Project != "" || got.Location != "" || got.Labels != nil {
		t.Errorf("nil-proto must produce zero result; got %+v", got)
	}
}

// TestWrapSearchAllError_UnauthenticatedSuggestsADCRefresh covers
// #365: the raw gRPC Unauthenticated message ("invalid_grant /
// invalid_rapt") is unactionable to operators not fluent in Google
// auth internals. The wrap replaces it with a concrete command to
// run, preserving the original error in parentheses so log-search
// still works.
func TestWrapSearchAllError_UnauthenticatedSuggestsADCRefresh(t *testing.T) {
	t.Parallel()
	raw := status.Error(codes.Unauthenticated, `transport: per-RPC creds failed due to error: auth: "invalid_grant" "reauth related error (invalid_rapt)"`)
	wrapped := wrapSearchAllError(raw)
	got := wrapped.Error()
	if !strings.Contains(got, "gcloud auth application-default login") {
		t.Errorf("wrapped error must contain the ADC-refresh command\n--- got ---\n%s", got)
	}
	// Original error message preserved for log search.
	if !strings.Contains(got, "invalid_rapt") {
		t.Errorf("original error must be preserved for log search\n--- got ---\n%s", got)
	}
	// %w wrap preserves the gRPC status in the chain: errors.Is
	// against the original status error returns true. A regression to
	// %v would break this.
	if !errors.Is(wrapped, raw) {
		t.Errorf("errors.Is must reach the wrapped gRPC status error (broken by %%v regression?)")
	}
}

// TestWrapSearchAllError_PermissionDeniedNotEnabledSuggestsQuotaProject
// covers the second auth-failure mode: Cloud Asset API not enabled on
// the ADC quota project. The 2026-05-10 smoke surfaced this as a
// confusing failure because the user assumed the API needs to be
// enabled on the scope project (the one they're searching), not the
// quota project (the one that owns their ADC credentials).
func TestWrapSearchAllError_PermissionDeniedNotEnabledSuggestsQuotaProject(t *testing.T) {
	t.Parallel()
	raw := status.Error(codes.PermissionDenied, "Cloud Asset API has not been used in project foo or it is disabled. API not enabled.")
	wrapped := wrapSearchAllError(raw)
	got := wrapped.Error()
	if !strings.Contains(got, "ADC quota project") {
		t.Errorf("wrapped error must reference the ADC quota project\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "cloudasset.googleapis.com") {
		t.Errorf("wrapped error must name the API to enable\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "API not enabled") {
		t.Errorf("original error must be preserved\n--- got ---\n%s", got)
	}
}

// TestWrapSearchAllError_PermissionDeniedIAMPassThrough pins the
// narrowness of the not-enabled wrap: a generic PermissionDenied
// (e.g. principal lacks cloudasset.assets.searchAllResources) is NOT
// transformed because the "enable the API" message would be wrong.
// The original error is wrapped in the default "search all resources"
// prefix.
func TestWrapSearchAllError_PermissionDeniedIAMPassThrough(t *testing.T) {
	t.Parallel()
	raw := status.Error(codes.PermissionDenied, "User 'sam@example.com' does not have cloudasset.assets.searchAllResources permission on projects/foo.")
	wrapped := wrapSearchAllError(raw)
	got := wrapped.Error()
	if strings.Contains(got, "ADC quota project") {
		t.Errorf("generic PermissionDenied must NOT trigger the not-enabled wrap\n--- got ---\n%s", got)
	}
	if !strings.HasPrefix(got, "search all resources: ") {
		t.Errorf("default prefix must apply\n--- got ---\n%s", got)
	}
}

// TestWrapSearchAllError_OtherCodesPassThrough pins the
// default-pass-through path. Any non-Unauthenticated /
// non-PermissionDenied gRPC code (Internal, ResourceExhausted,
// DeadlineExceeded, Unavailable) gets the original error verbatim —
// we don't have actionable advice for those, and a wrong wrap would
// mask real bugs. Table-driven so a regression that special-cased
// (say) ResourceExhausted is caught.
func TestWrapSearchAllError_OtherCodesPassThrough(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		code codes.Code
		msg  string
	}{
		{"Internal", codes.Internal, "internal server error"},
		{"ResourceExhausted", codes.ResourceExhausted, "quota exceeded"},
		{"DeadlineExceeded", codes.DeadlineExceeded, "context deadline exceeded"},
		{"Unavailable", codes.Unavailable, "service unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := status.Error(tc.code, tc.msg)
			wrapped := wrapSearchAllError(raw)
			got := wrapped.Error()
			if !strings.Contains(got, tc.msg) {
				t.Errorf("non-auth errors must pass through verbatim\n--- got ---\n%s", got)
			}
			if strings.Contains(got, "gcloud auth") || strings.Contains(got, "ADC quota project") {
				t.Errorf("non-auth errors must NOT trigger auth-wraps\n--- got ---\n%s", got)
			}
		})
	}
}

// TestWrapSearchAllError_NonStatusErrorPassThrough pins the
// non-gRPC-error path. A plain `errors.New("oops")` (not wrapped via
// status.Error) is not a status, so the type-switch lookup fails and
// we fall through to the default wrap.
func TestWrapSearchAllError_NonStatusErrorPassThrough(t *testing.T) {
	t.Parallel()
	raw := errors.New("connection refused")
	wrapped := wrapSearchAllError(raw)
	got := wrapped.Error()
	if !strings.Contains(got, "connection refused") {
		t.Errorf("non-status errors must pass through verbatim\n--- got ---\n%s", got)
	}
	if !strings.HasPrefix(got, "search all resources: ") {
		t.Errorf("default prefix must apply\n--- got ---\n%s", got)
	}
}

// TestNewRealAssetSearcher_AcceptsOptions pins the #445 variadic
// option.ClientOption contract: NewRealAssetSearcher must thread caller
// opts through to asset.NewClient so multi-tenant server-side consumers
// can inject per-request BYOC credentials via option.WithTokenSource
// without touching process-global state (GOOGLE_APPLICATION_CREDENTIALS).
//
// asset.NewClient's gRPC dial is lazy — the constructor accepts arbitrary
// transport opts (here option.WithEndpoint to a sentinel value, plus
// option.WithoutAuthentication so the client doesn't try to resolve ADC
// in unit-test environments where it may not be configured) and returns
// successfully without contacting the endpoint. A successful return is
// proof that the variadic was forwarded: if the opts were dropped on
// the floor, the client would still try ADC and fail in CI sandboxes
// without GOOGLE_APPLICATION_CREDENTIALS set.
func TestNewRealAssetSearcher_AcceptsOptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// option.WithoutAuthentication short-circuits the credential
	// resolver — proves the variadic is forwarded because dropping
	// it would force ADC discovery in this hermetic test process.
	// option.WithEndpoint exercises a second opt of a different
	// kind (transport vs auth) so the variadic accepts a slice, not
	// just one item.
	s, err := NewRealAssetSearcher(ctx,
		option.WithoutAuthentication(),
		option.WithEndpoint("localhost:0"),
	)
	if err != nil {
		t.Fatalf("NewRealAssetSearcher(opts...) must construct: %v", err)
	}
	if s == nil || s.client == nil {
		t.Fatalf("searcher must be non-nil with a non-nil client; got %+v", s)
	}
	// Don't leak the gRPC conn even though it's lazy — Close is
	// idempotent and the symmetric path matches the production
	// caller (cmd/insideout-import/discover.go).
	if err := s.Close(); err != nil {
		t.Errorf("Close must succeed on a fresh searcher: %v", err)
	}
}

// TestNewRealAssetSearcher_NoOptsKeepsADCFallback pins that callers
// passing no opts (the CLI use case) continue to get the ADC-backed
// client construction. Skipped on hosts without ADC configured
// (e.g. CI without GOOGLE_APPLICATION_CREDENTIALS) — asset.NewClient
// eagerly resolves credentials, so a constructor call without ADC fails
// at construction time. The contract this test locks is "zero-opt call
// site keeps working when ADC is available" so a future refactor that
// made opts required (e.g. by introducing a non-variadic signature)
// breaks here on dev machines + the GCP-credentialled CI lanes that
// can run this path.
func TestNewRealAssetSearcher_NoOptsKeepsADCFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := google.FindDefaultCredentials(ctx); err != nil {
		t.Skipf("Application Default Credentials not configured; the zero-opt construction path can't be exercised here. ADC error: %v", err)
	}
	s, err := NewRealAssetSearcher(ctx)
	if err != nil {
		t.Fatalf("zero-opt NewRealAssetSearcher must construct (ADC fallback path): %v", err)
	}
	if s == nil || s.client == nil {
		t.Fatalf("searcher must be non-nil with a non-nil client; got %+v", s)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close must succeed: %v", err)
	}
}
