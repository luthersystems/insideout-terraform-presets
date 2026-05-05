// Tests for protoNormalize. Lock the protojson conversion contract
// for proto.Message and proto.Message slices, and confirm non-proto
// shapes pass through unchanged.
//
// Mutation-resistant: each test asserts a specific protojson signature
// (e.g. "604800s" for *durationpb.Duration, named-enum string for
// GKE cluster status). A regression that swapped protojson for plain
// encoding/json would silently fail the existing GCP extractors;
// these tests catch the regression upstream.
//
// Lifted behaviorally from
// reliable/internal/agentapi/proto_normalize_test.go. The end-to-end
// extractor coupling tests there exercise reliable's drift extractors
// (which live in reliable/internal/), so they don't lift.

package inspect

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	cloudbuildpb "cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	pubsubpb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestProtoNormalize_PubSubTopicSlice locks the protojson contract
// for proto well-known types. Without normalization,
// *durationpb.Duration round-trips via encoding/json as
// `{"seconds":604800}` and consumers reading `messageRetentionDuration`
// as a string get "" via fmt fallback.
func TestProtoNormalize_PubSubTopicSlice(t *testing.T) {
	t.Parallel()
	topics := []*pubsubpb.Topic{{
		Name:                     "projects/demo/topics/events",
		MessageRetentionDuration: durationpb.New(7 * 24 * time.Hour),
	}}

	out := protoNormalize(topics)
	slice, ok := out.([]any)
	if !ok {
		t.Fatalf("[]*pubsubpb.Topic → %T, want []any of map[string]any", out)
	}
	if len(slice) != 1 {
		t.Fatalf("got %d elements, want 1", len(slice))
	}

	first, ok := slice[0].(map[string]any)
	if !ok {
		t.Fatalf("element[0] = %T, want map[string]any", slice[0])
	}
	if got := first["name"]; got != "projects/demo/topics/events" {
		t.Errorf("name = %v, want %q", got, "projects/demo/topics/events")
	}
	// protojson.Duration → "604800s" (NOT {"seconds":604800}).
	if got := first["messageRetentionDuration"]; got != "604800s" {
		t.Errorf("messageRetentionDuration = %v, want %q (Duration must protojson-marshal to string form)", got, "604800s")
	}
}

// TestProtoNormalize_GKEClusterSlice locks the enum-naming contract.
// Without normalization, containerpb.Cluster_RUNNING (enum value 2)
// round-trips as the integer 2; consumers reading status via
// `getString(c, "status")` would get "2" via fmt fallback.
func TestProtoNormalize_GKEClusterSlice(t *testing.T) {
	t.Parallel()
	clusters := []*containerpb.Cluster{{
		Name:                 "demo-gke",
		Status:               containerpb.Cluster_RUNNING,
		Location:             "us-central1",
		CurrentNodeCount:     3,
		CurrentMasterVersion: "1.29.4-gke.1043000",
		Autopilot:            &containerpb.Autopilot{Enabled: true},
	}}

	out := protoNormalize(clusters)
	slice, ok := out.([]any)
	if !ok {
		t.Fatalf("[]*containerpb.Cluster → %T, want []any", out)
	}
	first, ok := slice[0].(map[string]any)
	if !ok {
		t.Fatalf("element[0] = %T, want map[string]any", slice[0])
	}

	// Named enum string, not integer.
	if got := first["status"]; got != "RUNNING" {
		t.Errorf("status = %v, want %q (named enum, not integer)", got, "RUNNING")
	}
	// lowerCamelCase, not snake_case.
	if got, _ := first["currentNodeCount"].(float64); got != 3 {
		t.Errorf("currentNodeCount = %v, want 3 (lowerCamelCase, JSON-decoded as float64)", first["currentNodeCount"])
	}
	if got := first["currentMasterVersion"]; got != "1.29.4-gke.1043000" {
		t.Errorf("currentMasterVersion = %v, want %q", got, "1.29.4-gke.1043000")
	}
	autopilot, ok := first["autopilot"].(map[string]any)
	if !ok {
		t.Fatalf("autopilot = %T, want map[string]any (nested message normalizes recursively)", first["autopilot"])
	}
	if autopilot["enabled"] != true {
		t.Errorf("autopilot.enabled = %v, want true", autopilot["enabled"])
	}
}

// TestProtoNormalize_NonProtoSlicePassthrough — the inspector returns
// []string for Firestore (collection IDs) and Cloud Logging (log
// names), and pre-flattened []map[string]any for GCS. None of these
// are proto.Message slices; protoNormalize must pass them through
// unchanged.
func TestProtoNormalize_NonProtoSlicePassthrough(t *testing.T) {
	t.Parallel()

	// []string → unchanged
	in := []string{"users", "orders"}
	out := protoNormalize(in)
	gotStrings, ok := out.([]string)
	if !ok {
		t.Fatalf("[]string → %T, want []string (unchanged)", out)
	}
	if len(gotStrings) != 2 || gotStrings[0] != "users" || gotStrings[1] != "orders" {
		t.Errorf("[]string passthrough = %v, want %v", gotStrings, in)
	}

	// []map[string]any (GCS pre-flattened) → unchanged
	gcs := []map[string]any{
		{"name": "demo-bucket", "location": "US-CENTRAL1", "storageClass": "STANDARD"},
	}
	out = protoNormalize(gcs)
	gotMaps, ok := out.([]map[string]any)
	if !ok {
		t.Fatalf("[]map[string]any → %T, want []map[string]any (unchanged)", out)
	}
	if gotMaps[0]["storageClass"] != "STANDARD" {
		t.Errorf("[]map[string]any passthrough did not preserve content")
	}
}

// TestProtoNormalize_HandBuiltMapPassthrough — billing and list-
// actions return hand-built map[string]any. Pass through.
func TestProtoNormalize_HandBuiltMapPassthrough(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"project_id":      "demo",
		"billing_enabled": true,
	}
	out := protoNormalize(in)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("map[string]any → %T, want map[string]any", out)
	}
	if m["project_id"] != "demo" || m["billing_enabled"] != true {
		t.Errorf("hand-built map mutated: %v", m)
	}
}

// TestProtoNormalize_NestedProtoInMap — identity_platform list-
// providers wraps proto slices inside a map. Recursion normalizes
// each value.
func TestProtoNormalize_NestedProtoInMap(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"oauth_idp_configs":                   []*pubsubpb.Topic{{Name: "fake-as-marker"}},
		"default_supported_idp_configs_error": "permission denied",
	}
	out := protoNormalize(in)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("map[string]any → %T", out)
	}
	if m["default_supported_idp_configs_error"] != "permission denied" {
		t.Errorf("string value not preserved: %v", m["default_supported_idp_configs_error"])
	}
	slice, ok := m["oauth_idp_configs"].([]any)
	if !ok {
		t.Fatalf("nested proto slice not normalized: %T", m["oauth_idp_configs"])
	}
	first, ok := slice[0].(map[string]any)
	if !ok {
		t.Fatalf("nested element[0] = %T, want map[string]any", slice[0])
	}
	if first["name"] != "fake-as-marker" {
		t.Errorf("nested element name = %v, want %q", first["name"], "fake-as-marker")
	}
}

// TestProtoNormalize_HeterogeneousInterfaceSlicePassthrough — when an
// []any contains a mix of proto.Message and non-proto values, the
// function must pass the slice through unchanged rather than
// partially normalize it.
func TestProtoNormalize_HeterogeneousInterfaceSlicePassthrough(t *testing.T) {
	t.Parallel()
	mixed := []any{
		&pubsubpb.Topic{Name: "projects/demo/topics/x"},
		map[string]any{"sentinel": "non-proto"},
	}
	out := protoNormalize(mixed)
	slice, ok := out.([]any)
	if !ok {
		t.Fatalf("[]any → %T, want []any", out)
	}
	if len(slice) != 2 {
		t.Fatalf("got %d elements, want 2", len(slice))
	}
	// Second element must still be the original map (not normalized).
	second, ok := slice[1].(map[string]any)
	if !ok {
		t.Fatalf("element[1] = %T, want map[string]any (passed through)", slice[1])
	}
	if second["sentinel"] != "non-proto" {
		t.Errorf("non-proto element corrupted: %v", second)
	}
}

// TestProtoNormalize_NilAndEmpty — defensive: nil and empty inputs
// must be handled without panicking. The original form of this test
// in reliable wrapped already-evaluated `out` in NotPanics, which was
// a tautology — by the time the closure ran, the panic (if any) had
// already happened. This version actually wraps the call.
func TestProtoNormalize_NilAndEmpty(t *testing.T) {
	t.Parallel()
	if got := protoNormalize(nil); got != nil {
		t.Errorf("protoNormalize(nil) = %v, want nil", got)
	}

	// Empty proto slice — the function returns the original slice (no
	// elements to normalize), and downstream JSON encoding emits []
	// either way. We only require no panic.
	mustNotPanic(t, "empty proto slice", func() {
		var empty []*pubsubpb.Topic
		_ = protoNormalize(empty)
	})

	// Typed slice containing a nil proto pointer. (*pubsubpb.Topic)(nil)
	// satisfies proto.Message (typed nil), so normalizeProtoSlice will
	// try to marshal it; protojson.Marshal returns an error on nil
	// messages, protoMessageToMap returns (nil, false), and
	// normalizeProtoSlice gives up — passing the original slice
	// through unchanged.
	mustNotPanic(t, "typed slice with nil proto pointer", func() {
		withNil := []*pubsubpb.Topic{nil}
		_ = protoNormalize(withNil)
	})
}

// TestProtoNormalize_AwsCompatibility — confirms protoNormalize is a
// no-op for AWS SDK shapes (which use UpperCamelCase JSON tags via
// aws-sdk-go-v2 struct tags, NOT proto.Message). If this test fails,
// we've accidentally started normalizing AWS results — which would
// silently break every AWS extractor's reads.
func TestProtoNormalize_AwsCompatibility(t *testing.T) {
	t.Parallel()
	in := []ec2types.Instance{{
		InstanceId:   awsString("i-1234567890abcdef0"),
		InstanceType: ec2types.InstanceTypeT3Large,
	}}
	out := protoNormalize(in)

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"InstanceId"`) {
		t.Errorf("AWS shapes must keep UpperCamelCase JSON tags after protoNormalize passthrough; got %s", got)
	}
	if strings.Contains(got, `"instanceId"`) {
		t.Errorf("AWS shapes must NOT be normalized to lowerCamelCase; got %s", got)
	}
}

// TestProtoNormalize_CloudBuild_ProtoEnum locks one more proto shape
// — Cloud Build's BuildTrigger with a oneof field. Stand-in for
// "third-most-complex GCP shape" coverage; without this, a regression
// in oneof field marshaling would only surface in production.
func TestProtoNormalize_CloudBuild_ProtoEnum(t *testing.T) {
	t.Parallel()
	triggers := []*cloudbuildpb.BuildTrigger{{
		Name:          "deploy-on-main",
		BuildTemplate: &cloudbuildpb.BuildTrigger_Filename{Filename: "cloudbuild.yaml"},
		Disabled:      true,
	}}
	out := protoNormalize(triggers)
	slice, ok := out.([]any)
	if !ok {
		t.Fatalf("[]*BuildTrigger → %T, want []any", out)
	}
	first, ok := slice[0].(map[string]any)
	if !ok {
		t.Fatalf("element[0] = %T, want map[string]any", slice[0])
	}
	if first["filename"] != "cloudbuild.yaml" {
		t.Errorf("filename = %v, want %q (oneof field marshaled into top-level key)", first["filename"], "cloudbuild.yaml")
	}
	if first["disabled"] != true {
		t.Errorf("disabled = %v, want true", first["disabled"])
	}
}

// awsString — local helper for *string fields on AWS SDK types.
func awsString(s string) *string { return &s }

// mustNotPanic runs fn and fails the test if it panics. Used in place
// of testify's assert.NotPanics.
func mustNotPanic(t *testing.T, label string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: unexpected panic: %v", label, r)
		}
	}()
	fn()
}
