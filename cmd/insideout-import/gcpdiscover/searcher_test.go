package gcpdiscover

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"cloud.google.com/go/asset/apiv1/assetpb"
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

// TestAssetResultFromProto_NilProtoSafeFields pins the GetX accessor
// idioms — every proto getter on a nil/zero ResourceSearchResult
// returns the zero value of its field type. A future refactor that
// replaced `r.GetName()` with `r.Name` would panic on a nil proto;
// asserting on a zero-valued proto guards that.
func TestAssetResultFromProto_NilProtoSafeFields(t *testing.T) {
	t.Parallel()
	got := assetResultFromProto(&assetpb.ResourceSearchResult{})
	if got.Name != "" || got.AssetType != "" || got.Project != "" || got.Location != "" || got.Labels != nil {
		t.Errorf("zero-proto must produce zero result; got %+v", got)
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
// non-PermissionDenied gRPC code (e.g. ResourceExhausted, Internal)
// gets the original error verbatim — we don't have actionable advice
// for those, and a wrong wrap would mask real bugs.
func TestWrapSearchAllError_OtherCodesPassThrough(t *testing.T) {
	t.Parallel()
	raw := status.Error(codes.Internal, "internal server error")
	wrapped := wrapSearchAllError(raw)
	got := wrapped.Error()
	if !strings.Contains(got, "internal server error") {
		t.Errorf("non-auth errors must pass through verbatim\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "gcloud auth") || strings.Contains(got, "ADC quota project") {
		t.Errorf("non-auth errors must NOT trigger auth-wraps\n--- got ---\n%s", got)
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
