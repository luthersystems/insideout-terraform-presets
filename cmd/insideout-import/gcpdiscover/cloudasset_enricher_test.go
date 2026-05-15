package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestCamelToSnakeGCP pins the renamer behavior the enricher depends on
// for translating GCP REST API lowerCamelCase property keys into the
// snake_case json tags the generated Layer-1 structs declare. A drift
// in the renamer would silently miss every CAI field whose name doesn't
// round-trip — the unit test surfaces the regression alongside the
// cases it would have caught.
func TestCamelToSnakeGCP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"name", "name"},
		{"selfLink", "self_link"},
		{"machineType", "machine_type"},
		{"canIpForward", "can_ip_forward"},
		{"creationTimestamp", "creation_timestamp"},
		{"kmsKeyName", "kms_key_name"},
		// Acronym at start (UpperCamel pattern GCP uses in a few
		// historical fields like forwarding-rule IPAddress / IPProtocol).
		{"IPAddress", "ip_address"},
		{"IPProtocol", "ip_protocol"},
		// Trailing acronym.
		{"bucketURL", "bucket_url"},
		// Pure numeric segment.
		{"ipv4Range", "ipv4_range"},
	}
	for _, tc := range cases {
		got := camelToSnakeGCP(tc.in)
		assert.Equalf(t, tc.want, got, "camelToSnakeGCP(%q)", tc.in)
	}
}

// TestShapeCAIForLayer1Recursive pins the shape transform: scalar
// leaves get wrapped in {"literal": …} envelopes (so Value[T] can
// decode them), every nested map's keys are renamed to snake_case,
// and map list elements have their keys renamed too. Without this
// contract, bare CAI scalars (e.g. `"canIpForward": false`) would fail
// to decode against the Layer-1 *Value[bool] fields.
func TestShapeCAIForLayer1Recursive(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"name":         "my-vm",
		"machineType":  "https://www.googleapis.com/compute/v1/projects/p/zones/z/machineTypes/e2-medium",
		"canIpForward": false,
		"networkInterfaces": []any{
			map[string]any{
				"name":    "nic0",
				"network": "https://www.googleapis.com/compute/v1/projects/p/global/networks/default",
			},
		},
		"labels": map[string]any{
			"project": "io-abc",
		},
	}
	out := shapeCAIForLayer1(in)

	// Scalar leaves get wrapped.
	name, ok := out["name"].(map[string]any)
	require.Truef(t, ok, "name should be wrapped, got %T", out["name"])
	assert.Equal(t, "my-vm", name["literal"])

	machineType, ok := out["machine_type"].(map[string]any)
	require.Truef(t, ok, "machine_type should be wrapped, got %T", out["machine_type"])
	assert.Contains(t, machineType["literal"], "e2-medium")

	// Snake_case rename for the bool field.
	canIp, ok := out["can_ip_forward"].(map[string]any)
	require.Truef(t, ok, "can_ip_forward should be wrapped, got %T", out["can_ip_forward"])
	assert.Equal(t, false, canIp["literal"])

	// Nested-list element: keys renamed + leaves wrapped.
	nis, ok := out["network_interfaces"].([]any)
	require.True(t, ok)
	require.Len(t, nis, 1)
	ni, ok := nis[0].(map[string]any)
	require.True(t, ok)
	nicName, ok := ni["name"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "nic0", nicName["literal"])

	// Inner map: keys renamed.
	labels, ok := out["labels"].(map[string]any)
	require.True(t, ok)
	proj, ok := labels["project"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "io-abc", proj["literal"])
}

// fakeCAIGet is a closure-style GetByName fake. The test sets
// gotScope/gotAssetType/gotFullName from the inputs it receives so each
// test case can assert on the requested arguments without a Cloud Asset
// client. Returns the configured `data` map (or `err`) verbatim.
type fakeCAIGet struct {
	gotScope, gotAssetType, gotFullName string
	data                                map[string]any
	err                                 error
}

func (f *fakeCAIGet) call(_ context.Context, scope, assetType, fullName string) (map[string]any, error) {
	f.gotScope = scope
	f.gotAssetType = assetType
	f.gotFullName = fullName
	return f.data, f.err
}

// TestCloudAssetEnricher_Enrich_ComputeInstance exercises the full
// Enrich flow against a synthetic compute.googleapis.com/Instance CAI
// payload and asserts the resulting ir.Attrs round-trips through
// generated.UnmarshalAttrs into the typed GoogleComputeInstance struct
// with the expected scalar values. This is the load-bearing contract
// that justifies the json-tag codegen change in step 1 of #490 for the
// GCP side — and the PoC for HYBRID match-rate.
func TestCloudAssetEnricher_Enrich_ComputeInstance(t *testing.T) {
	t.Parallel()
	fake := &fakeCAIGet{
		data: map[string]any{
			"name":              "my-vm",
			"description":       "demo vm",
			"machineType":       "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/machineTypes/e2-medium",
			"canIpForward":      false,
			"creationTimestamp": "2024-01-01T00:00:00.000-08:00",
			"selfLink":          "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instances/my-vm",
			"zone":              "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a",
			"hostname":          "my-vm.example.com",
			"cpuPlatform":       "Intel Cascade Lake",
			"labels":            map[string]any{"project": "io-abc"},
		},
	}
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_instance",
			ProjectID: "real-proj",
			Address:   "google_compute_instance.demo",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm",
			},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	assert.Equal(t, "projects/real-proj", fake.gotScope)
	assert.Equal(t, "compute.googleapis.com/Instance", fake.gotAssetType)
	assert.Equal(t, "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm", fake.gotFullName)

	decoded, err := generated.UnmarshalAttrs("google_compute_instance", ir.Attrs)
	require.NoError(t, err)
	inst, ok := decoded.(*generated.GoogleComputeInstance)
	require.True(t, ok, "decoded type is %T, want *GoogleComputeInstance", decoded)

	// Fields whose CAI lowerCamelCase name matches the snake_case TF tag
	// after the renamer round-trip directly.
	require.NotNil(t, inst.Name)
	require.NotNil(t, inst.Name.Literal)
	assert.Equal(t, "my-vm", *inst.Name.Literal)

	require.NotNil(t, inst.Description)
	require.NotNil(t, inst.Description.Literal)
	assert.Equal(t, "demo vm", *inst.Description.Literal)

	require.NotNil(t, inst.MachineType)
	require.NotNil(t, inst.MachineType.Literal)
	assert.Contains(t, *inst.MachineType.Literal, "e2-medium")

	require.NotNil(t, inst.CanIpForward)
	require.NotNil(t, inst.CanIpForward.Literal)
	assert.Equal(t, false, *inst.CanIpForward.Literal)

	require.NotNil(t, inst.SelfLink)
	require.NotNil(t, inst.SelfLink.Literal)
	assert.Contains(t, *inst.SelfLink.Literal, "/instances/my-vm")

	require.NotNil(t, inst.Hostname)
	require.NotNil(t, inst.Hostname.Literal)
	assert.Equal(t, "my-vm.example.com", *inst.Hostname.Literal)

	// Labels map round-trips into the typed map[string]*Value[string] field.
	require.NotNil(t, inst.Labels)
	require.NotNil(t, inst.Labels["project"])
	require.NotNil(t, inst.Labels["project"].Literal)
	assert.Equal(t, "io-abc", *inst.Labels["project"].Literal)
}

// TestCloudAssetEnricher_Enrich_NilCloudAsset_FromClients asserts the
// production wiring path: when the embedded `fetch` is nil and
// EnrichClients.CloudAsset is also nil, Enrich returns
// ErrEnrichClientUnavailable so EnrichAttributes can downgrade to a
// per-resource warning rather than aborting the batch.
func TestCloudAssetEnricher_Enrich_NilCloudAsset_FromClients(t *testing.T) {
	t.Parallel()
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_instance",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm",
			},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

// TestCloudAssetEnricher_Enrich_NotFound exercises the soft-fail path:
// the underlying searcher's ErrNotFound is wrapped and re-emitted as
// ErrNotFound so EnrichAttributes can drop the resource from the batch
// result without failing the whole run. The most-likely real-world
// cause is a resource deleted between the discover and enrich stages,
// or CAI eventual-consistency lag.
func TestCloudAssetEnricher_Enrich_NotFound(t *testing.T) {
	t.Parallel()
	// Wrap ErrNotFound in the canonical fmt.Errorf("...: %w", ErrNotFound)
	// chain so the enricher detects it via errors.Is. Joined with an
	// inner message to mimic the searcher's real wrap shape ("cloud
	// asset getbyname <type> <name>: <msg>: <ErrNotFound>").
	fake := &fakeCAIGet{
		err: errors.Join(errors.New("cloud asset getbyname: simulated miss"), ErrNotFound),
	}
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_instance",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm",
			},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestCloudAssetEnricher_Enrich_NoAssetName asserts that the enricher
// rejects an Identity with no asset_name and no //-prefixed ImportID
// loudly. Falling through silently would later surface as a CAI
// INVALID_ARGUMENT; explicit detection at the dispatch site puts the
// misconfiguration in the test surface.
func TestCloudAssetEnricher_Enrich_NoAssetName(t *testing.T) {
	t.Parallel()
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", (&fakeCAIGet{}).call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_instance",
			ProjectID: "real-proj",
			Address:   "google_compute_instance.demo",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive CAI asset name")
}

// TestCloudAssetEnricher_Enrich_NoProjectID asserts that the enricher
// rejects an Identity with no ProjectID and no fallback. The scope
// derivation must succeed before issuing the CAI call.
func TestCloudAssetEnricher_Enrich_NoProjectID(t *testing.T) {
	t.Parallel()
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", (&fakeCAIGet{}).call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:    "google_compute_instance",
			Address: "google_compute_instance.demo",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm",
			},
			// No ProjectID set.
		},
	}
	// EnrichClients also empty → no fallback project either.
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive CAI scope")
}

// TestCloudAssetEnricher_Enrich_RealAPIError asserts that an arbitrary
// non-NotFound error from the underlying fetch propagates up wrapped
// but un-translated, so EnrichAttributes treats it as a real error and
// includes it in the aggregated batch failure.
func TestCloudAssetEnricher_Enrich_RealAPIError(t *testing.T) {
	t.Parallel()
	upstream := errors.New("permission denied")
	fake := &fakeCAIGet{err: upstream}
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_instance",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm",
			},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	assert.NotErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.ErrorIs(t, err, upstream)
}

// TestCloudAssetEnricher_EnrichByID exercises the ByIDEnricher entry
// point with the same fetch + mapping path as Enrich, asserting the
// returned raw JSON round-trips into the typed struct.
func TestCloudAssetEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	fake := &fakeCAIGet{
		data: map[string]any{
			"name":     "my-vm",
			"selfLink": "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instances/my-vm",
		},
	}
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", fake.call)
	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:      "google_compute_instance",
		ProjectID: "real-proj",
		NativeIDs: map[string]string{
			"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm",
		},
	}, EnrichClients{})
	require.NoError(t, err)
	decoded, err := generated.UnmarshalAttrs("google_compute_instance", raw)
	require.NoError(t, err)
	inst, ok := decoded.(*generated.GoogleComputeInstance)
	require.True(t, ok)
	require.NotNil(t, inst.Name)
	require.NotNil(t, inst.Name.Literal)
	assert.Equal(t, "my-vm", *inst.Name.Literal)
}

// TestCloudAssetEnricher_EnrichByID_NilIdentity asserts the
// programmer-error case (caller passed nil) is reported clearly rather
// than panicking inside the fetch call.
func TestCloudAssetEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", (&fakeCAIGet{}).call)
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

// TestCloudAssetEnricher_ResourceType pins the trivial accessor so a
// refactor that loses the field is caught at unit-test time.
func TestCloudAssetEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newCloudAssetEnricher("google_pubsub_topic", "pubsub.googleapis.com/Topic", nil)
	assert.Equal(t, "google_pubsub_topic", enr.ResourceType())
}

// TestCloudAssetEnricher_Enrich_UnknownTFType pins the wiring-bug
// detection: if a caller constructs an enricher for a TF type that
// isn't registered in pkg/composer/imported/generated, the enricher
// must report the missing registration cleanly rather than silently
// emitting raw CAI-shaped JSON the downstream UnmarshalAttrs would
// later reject.
func TestCloudAssetEnricher_Enrich_UnknownTFType(t *testing.T) {
	t.Parallel()
	fake := &fakeCAIGet{
		data: map[string]any{"name": "test"},
	}
	enr := newCloudAssetEnricher("google_does_not_exist", "phony.googleapis.com/Type", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_does_not_exist",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{"asset_name": "//phony.googleapis.com/projects/real-proj/things/x"},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal into google_does_not_exist")
}

// TestCloudAssetEnricher_Enrich_RawAttrs_IsValidJSONWithSnakeKeys
// guards the wire-format contract: after the json-tags codegen change,
// ir.Attrs uses lowercase snake_case keys for every top-level
// attribute. A regression that drops the json tag emission would
// silently revert to CamelCase Go-field-name keys, reintroducing the
// renamer-projection workaround and a coverage gap on every multi-word
// attribute name.
func TestCloudAssetEnricher_Enrich_RawAttrs_IsValidJSONWithSnakeKeys(t *testing.T) {
	t.Parallel()
	fake := &fakeCAIGet{
		data: map[string]any{
			"name":              "my-vm",
			"canIpForward":      true,
			"creationTimestamp": "2024-01-01T00:00:00Z",
		},
	}
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_instance",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/my-vm",
			},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(ir.Attrs, &top))
	assert.Contains(t, top, "name")
	assert.Contains(t, top, "can_ip_forward")
	assert.Contains(t, top, "creation_timestamp")
	for k := range top {
		assert.Equalf(t, strings.ToLower(k), k, "expected lowercase top-level key, got %q", k)
	}
}

// TestCloudAssetEnricher_PoC_ComputeInstance_FieldMatchRate is the
// HYBRID PoC pinned as a unit test. It builds a representative CAI
// Resource.Data payload for a compute.googleapis.com/Instance asset,
// runs the enricher's full Enrich+round-trip, and computes the
// match-rate of (CAI fields that land in a typed Layer-1 slot) /
// (total CAI fields the payload exposed). The threshold below pins
// the floor — a regression that drops the json tags or breaks the
// snake_case renamer would crash the rate before the test ever fails
// in CI.
//
// AWS Cloud Control HYBRID PoC measured 57% on aws_cloudwatch_log_group;
// GCP CAI is projected higher because (a) CAI returns native REST JSON
// which already uses lowerCamelCase, and (b) labels are map[string]string
// in both API and TF. Threshold: 70% — high enough to lock in the win,
// low enough to leave room for known divergences (self_link URL format
// vs bare names; region/zone URLs; computed-only fields the policy
// elides downstream). PoC outcome documented in .tmp/cai-enricher-poc.md.
func TestCloudAssetEnricher_PoC_ComputeInstance_FieldMatchRate(t *testing.T) {
	t.Parallel()
	// Realistic compute.googleapis.com/Instance CAI payload. Field set
	// pulled from the Compute Engine v1 REST API's Instance resource
	// representation per https://cloud.google.com/compute/docs/reference/rest/v1/instances#resource:-instance.
	// Selected the broadly-applicable scalar + nested fields a real
	// CAI versionedResources payload would surface; deliberately
	// excluded service-managed metadata (creationTimestamp, id, status)
	// to keep the rate honest against the decision-#5 emission rule.
	cai := map[string]any{
		// Scalar top-level fields.
		"name":                    "demo-vm",
		"description":             "demo virtual machine",
		"machineType":             "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/machineTypes/e2-medium",
		"canIpForward":            false,
		"deletionProtection":      false,
		"hostname":                "demo-vm.example.com",
		"cpuPlatform":             "Intel Cascade Lake",
		"selfLink":                "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a/instances/demo-vm",
		"zone":                    "https://www.googleapis.com/compute/v1/projects/p/zones/us-central1-a",
		"minCpuPlatform":          "Intel Cascade Lake",
		"creationTimestamp":       "2024-01-01T00:00:00.000-08:00",
		"keyRevocationActionType": "NONE",
		// labels (TF: map[string]*Value[string], CAI: map[string]string).
		"labels": map[string]any{
			"project": "io-abc",
			"env":     "prod",
		},
		// network tags ([]string in both CAI and TF — note: this is
		// `tags.items` in the REST API but CAI surfaces it as a flat
		// `tags` array; TF stores it as a `tags` set of strings).
		// Excluded here because the TF field name is `tags` but CAI
		// surfaces it as a nested {items: []} object — a known
		// divergence the Normalizer hooks follow-up will bridge.
	}

	fake := &fakeCAIGet{data: cai}
	enr := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_instance",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-central1-a/instances/demo-vm",
			},
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

	// Decode into the typed struct.
	decoded, err := generated.UnmarshalAttrs("google_compute_instance", ir.Attrs)
	require.NoError(t, err)
	inst, ok := decoded.(*generated.GoogleComputeInstance)
	require.True(t, ok)

	// Top-level CAI fields and the expected typed-struct populated state
	// after the enricher round-trip. A field is "matched" iff the typed
	// struct's corresponding slot is non-nil after round-trip.
	type check struct {
		caiField string
		matched  bool
	}
	checks := []check{
		{"name", inst.Name != nil},
		{"description", inst.Description != nil},
		{"machineType", inst.MachineType != nil},
		{"canIpForward", inst.CanIpForward != nil},
		{"deletionProtection", inst.DeletionProtection != nil},
		{"hostname", inst.Hostname != nil},
		{"cpuPlatform", inst.CPUPlatform != nil},
		{"selfLink", inst.SelfLink != nil},
		{"zone", inst.Zone != nil},
		{"minCpuPlatform", inst.MinCPUPlatform != nil},
		{"creationTimestamp", inst.CreationTimestamp != nil},
		{"keyRevocationActionType", inst.KeyRevocationActionType != nil},
		{"labels", len(inst.Labels) > 0},
	}

	matched := 0
	for _, c := range checks {
		if c.matched {
			matched++
		} else {
			t.Logf("PoC: unmatched CAI field %q (HYBRID gap; follow-up Normalizer hooks)", c.caiField)
		}
	}
	total := len(checks)
	rate := float64(matched) / float64(total)
	t.Logf("PoC compute_instance HYBRID match rate: %d/%d = %.0f%%", matched, total, rate*100)

	// Floor: 70% — comfortably above the AWS PoC's 57% on
	// aws_cloudwatch_log_group. Set high enough to lock in the win, low
	// enough that a single new divergence doesn't break CI before the
	// Normalizer hooks follow-up lands.
	const floor = 0.70
	if rate < floor {
		t.Errorf("PoC field match rate %.2f below floor %.2f — enricher coverage regressed", rate, floor)
	}
}

// Compile-time pin: cloudAssetEnricher must satisfy both
// AttributeEnricher and ByIDEnricher. A drift in either interface that
// drops one of these methods is caught at build time. (Mirrors the
// var-block in cloudasset_enricher.go.)
var _ AttributeEnricher = (*cloudAssetEnricher)(nil)
var _ ByIDEnricher = (*cloudAssetEnricher)(nil)
