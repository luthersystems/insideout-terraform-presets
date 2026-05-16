package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestCamelToSnakeGCP_TableDriven pins the lowerCamelCase →
// snake_case rename used by the CAI HYBRID enricher to bridge GCP
// REST JSON keys onto the generated Layer-1 struct's snake_case json
// tags. A mutation here (e.g. accidentally splitting acronym runs
// the wrong way) would silently break every field on every CAI-
// routed enricher.
func TestCamelToSnakeGCP_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		// Standard GCP lowerCamelCase patterns.
		{"selfLink", "self_link"},
		{"machineType", "machine_type"},
		{"canIpForward", "can_ip_forward"},
		{"creationTimestamp", "creation_timestamp"},
		{"name", "name"},
		{"labels", "labels"},

		// Acronym at the start (a handful of GCP fields use this).
		{"IPAddress", "ip_address"},
		{"IPProtocol", "ip_protocol"},

		// UpperCamelCase fallback (some legacy fields).
		{"ARNTag", "arn_tag"},
		{"KmsKeyName", "kms_key_name"},

		// Digits — pass-through chars adjacent to letters. The
		// algorithm treats letter-after-digit as a boundary
		// (digit → word transition), keeping ipv4-style names
		// readable.
		{"ipv4Range", "ipv4_range"},
		{"ipv6CidrRange", "ipv6_cidr_range"},

		// Empty input and single-char edge cases.
		{"", ""},
		{"a", "a"},
		{"A", "a"},

		// Hyphens / non-letters pass through unchanged. GCP REST
		// JSON keys don't typically contain hyphens, but the
		// algorithm should be safe regardless.
		{"foo-bar", "foo-bar"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := camelToSnakeGCP(tc.in)
			if got != tc.want {
				t.Errorf("camelToSnakeGCP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestShapeCAIForLayer1_LiteralEnvelopesScalars pins the
// scalar-leaf {"literal": …} wrap. Without it, generated.Value[T]
// fails to unmarshal a bare scalar (its UnmarshalJSON requires the
// {"literal": …} object envelope or an HCL function envelope, never
// a bare value).
func TestShapeCAIForLayer1_LiteralEnvelopesScalars(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"name":         "vm-1",
		"machineType":  "n1-standard-1",
		"canIpForward": false,
	}
	got := shapeCAIForLayer1(in)
	require.Contains(t, got, "name")
	require.Contains(t, got, "machine_type")
	require.Contains(t, got, "can_ip_forward")
	assert.Equal(t, map[string]any{"literal": "vm-1"}, got["name"])
	assert.Equal(t, map[string]any{"literal": "n1-standard-1"}, got["machine_type"])
	assert.Equal(t, map[string]any{"literal": false}, got["can_ip_forward"])
}

// TestShapeCAIForLayer1_NestedMapsRecurse pins that nested objects
// recurse into the same shape — the inner map's keys snake_case and
// its leaves get {"literal": …} wrapped, but the nested object
// itself does NOT get a literal wrap (the generated nested-block
// struct is a bare struct, not a Value[T]).
func TestShapeCAIForLayer1_NestedMapsRecurse(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"scheduling": map[string]any{
			"automaticRestart":  true,
			"onHostMaintenance": "MIGRATE",
		},
	}
	got := shapeCAIForLayer1(in)
	require.Contains(t, got, "scheduling")
	inner, ok := got["scheduling"].(map[string]any)
	require.True(t, ok, "nested object must stay a map, not get literal-wrapped")
	assert.Equal(t, map[string]any{"literal": true}, inner["automatic_restart"])
	assert.Equal(t, map[string]any{"literal": "MIGRATE"}, inner["on_host_maintenance"])
}

// TestShapeCAIForLayer1_NilInputReturnsNil pins the safe nil
// pass-through. A nil input must not panic the recursive walker.
func TestShapeCAIForLayer1_NilInputReturnsNil(t *testing.T) {
	t.Parallel()
	got := shapeCAIForLayer1(nil)
	assert.Nil(t, got)
}

// TestShapeCAIForLayer1_ListOfMapsRecurses pins the slice-of-objects
// path: lists of nested objects recurse on each element so their
// keys snake_case and their leaves get literal-wrapped.
func TestShapeCAIForLayer1_ListOfMapsRecurses(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"networkInterfaces": []any{
			map[string]any{
				"network":    "default",
				"subnetwork": "subnet-1",
			},
		},
	}
	got := shapeCAIForLayer1(in)
	require.Contains(t, got, "network_interfaces")
	list, ok := got["network_interfaces"].([]any)
	require.True(t, ok)
	require.Len(t, list, 1)
	first, ok := list[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"literal": "default"}, first["network"])
	assert.Equal(t, map[string]any{"literal": "subnet-1"}, first["subnetwork"])
}

// TestCloudAssetNameFromIdentity_Precedence pins the precedence of
// asset-name derivation: NativeIDs["asset_name"] (canonical) before
// the //-prefixed-ImportID fallback.
func TestCloudAssetNameFromIdentity_Precedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   *imported.ResourceIdentity
		want string
	}{
		{
			name: "asset_name wins over ImportID",
			in: &imported.ResourceIdentity{
				NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/p/zones/z/instances/n"},
				ImportID:  "//compute.googleapis.com/projects/OTHER/zones/z/instances/n",
			},
			want: "//compute.googleapis.com/projects/p/zones/z/instances/n",
		},
		{
			name: "ImportID fallback when starts with //",
			in: &imported.ResourceIdentity{
				ImportID: "//compute.googleapis.com/projects/p/zones/z/instances/n",
			},
			want: "//compute.googleapis.com/projects/p/zones/z/instances/n",
		},
		{
			name: "empty when both unset",
			in:   &imported.ResourceIdentity{},
			want: "",
		},
		{
			name: "nil-safe",
			in:   nil,
			want: "",
		},
		{
			name: "ImportID NOT starting with // is rejected",
			in: &imported.ResourceIdentity{
				ImportID: "projects/p/zones/z/instances/n",
			},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cloudAssetNameFromIdentity(tc.in)
			if got != tc.want {
				t.Errorf("cloudAssetNameFromIdentity = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCloudAssetScopeFromIdentity_Precedence pins the precedence of
// scope derivation: per-resource ProjectID before the run-level
// fallback.
func TestCloudAssetScopeFromIdentity_Precedence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		in              *imported.ResourceIdentity
		fallbackProject string
		want            string
	}{
		{
			name:            "identity.ProjectID wins",
			in:              &imported.ResourceIdentity{ProjectID: "real-proj"},
			fallbackProject: "fallback-proj",
			want:            "projects/real-proj",
		},
		{
			name:            "fallback when identity.ProjectID empty",
			in:              &imported.ResourceIdentity{},
			fallbackProject: "fallback-proj",
			want:            "projects/fallback-proj",
		},
		{
			name:            "empty when both unset",
			in:              &imported.ResourceIdentity{},
			fallbackProject: "",
			want:            "",
		},
		{
			name:            "nil identity OK",
			in:              nil,
			fallbackProject: "fallback-proj",
			want:            "projects/fallback-proj",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cloudAssetScopeFromIdentity(tc.in, tc.fallbackProject)
			if got != tc.want {
				t.Errorf("cloudAssetScopeFromIdentity = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCloudAssetEnricher_EnrichRoundTrip pins the end-to-end Enrich
// path for a small CAI payload. Uses an injected fetch closure (no
// real CAI client) and a real registered Layer-1 struct
// (GoogleComputeNetwork — the smallest CAI-routed struct in the
// registry). Asserts that the returned ir.Attrs round-trips back
// into the typed struct with at least the name and description
// populated, proving the lowerCamelCase → snake_case → Value[T]
// pipeline lands fields where the generated struct expects them.
func TestCloudAssetEnricher_EnrichRoundTrip(t *testing.T) {
	t.Parallel()
	// google_compute_network is the smallest CAI-routed Layer-1
	// struct in the registry — fewer than 20 fields, no nested
	// blocks. A field hit here proves the wire-format round-trip
	// works; structural shape issues would show up on the
	// snake_case rename or the Value[T] wrap.
	fetch := func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
		return map[string]any{
			"name":                  "io-test-net",
			"description":           "test network",
			"autoCreateSubnetworks": false,
			"routingConfig": map[string]any{
				"routingMode": "REGIONAL",
			},
		}, nil
	}
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", fetch)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_network",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/real-proj/global/networks/io-test-net",
			},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{})
	require.NoError(t, err)
	require.NotEmpty(t, ir.Attrs)

	// Round-trip back into the typed struct.
	decoded, err := generated.UnmarshalAttrs("google_compute_network", ir.Attrs)
	require.NoError(t, err)
	got, ok := decoded.(*generated.GoogleComputeNetwork)
	require.True(t, ok, "decoded must be *GoogleComputeNetwork")
	require.NotNil(t, got.Name)
	assert.Equal(t, "io-test-net", *got.Name.Literal)
	require.NotNil(t, got.Description)
	assert.Equal(t, "test network", *got.Description.Literal)
	require.NotNil(t, got.AutoCreateSubnetworks)
	assert.False(t, *got.AutoCreateSubnetworks.Literal)
}

// TestCloudAssetEnricher_EnrichByIDRoundTrip pins the ByIDEnricher
// entry-point: same wire path as Enrich, but returns the raw
// payload instead of mutating an IR. Used by the per-IR drift
// refresh path (pkg/imported.Provider.EnrichByID, #482).
func TestCloudAssetEnricher_EnrichByIDRoundTrip(t *testing.T) {
	t.Parallel()
	fetch := func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
		return map[string]any{
			"name": "io-test-net",
		}, nil
	}
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", fetch)
	id := &imported.ResourceIdentity{
		Type:      "google_compute_network",
		ProjectID: "real-proj",
		NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/real-proj/global/networks/io-test-net"},
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	decoded, err := generated.UnmarshalAttrs("google_compute_network", raw)
	require.NoError(t, err)
	got, ok := decoded.(*generated.GoogleComputeNetwork)
	require.True(t, ok)
	require.NotNil(t, got.Name)
	assert.Equal(t, "io-test-net", *got.Name.Literal)
}

// TestCloudAssetEnricher_NotFoundIsSoftFail pins the ErrNotFound
// passthrough. When the searcher reports the asset is gone (deleted
// between discovery and enrichment, or IAM gap), the enricher must
// wrap the error so EnrichAttributes treats it as a per-resource
// warning rather than a batch-fatal error.
func TestCloudAssetEnricher_NotFoundIsSoftFail(t *testing.T) {
	t.Parallel()
	fetch := func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
		return nil, fmt.Errorf("simulated: %w", ErrNotFound)
	}
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", fetch)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_network",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/real-proj/global/networks/gone"},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "ErrNotFound must remain in the error chain for EnrichAttributes to downgrade")
}

// TestCloudAssetEnricher_NilClientUnavailable pins the
// ErrEnrichClientUnavailable branch. When neither a test-injected
// fetch nor an EnrichClients.CloudAsset is wired, the enricher
// surfaces a typed unavailable error so the EnrichAttributes loop
// can downgrade to a per-resource warning.
func TestCloudAssetEnricher_NilClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_network",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/real-proj/global/networks/x"},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEnrichClientUnavailable))
}

// TestCloudAssetEnricher_MissingAssetNameErrors pins the
// can't-derive-identifier error. The CAI enricher relies on
// NativeIDs["asset_name"] (or an //-prefixed ImportID) to know
// which asset to fetch — an Identity missing both is a programmer
// error that should be loud at test time, not silent at runtime.
func TestCloudAssetEnricher_MissingAssetNameErrors(t *testing.T) {
	t.Parallel()
	fetch := func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
		t.Fatalf("fetch must not be invoked when asset_name is missing")
		return nil, nil
	}
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", fetch)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_network",
			ProjectID: "real-proj",
			// no NativeIDs, no //-prefixed ImportID
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive CAI asset name")
}

// TestCloudAssetEnricher_MissingScopeErrors pins the
// can't-derive-scope error. Same shape as the missing-asset-name
// case but exercises the scope branch. CAI rejects an empty scope
// with INVALID_ARGUMENT — wrapping it client-side keeps the error
// one frame closer to the misconfiguration.
func TestCloudAssetEnricher_MissingScopeErrors(t *testing.T) {
	t.Parallel()
	fetch := func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
		t.Fatalf("fetch must not be invoked when scope is empty")
		return nil, nil
	}
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", fetch)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_compute_network",
			NativeIDs: map[string]string{
				"asset_name": "//compute.googleapis.com/projects/x/global/networks/y",
			},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive CAI scope")
}

// TestCloudAssetEnricher_UnregisteredTypeErrors pins the
// generated-registry sanity check. An entry in
// cloudAssetTypeConfigs that points to a TF type without a matching
// generated.Google<Type> struct would silently emit raw CAI-shaped
// JSON — UnmarshalAttrs catches this and converts it to a hard error.
// The TestCloudAssetEnricherCoversEveryCAIRoutedType wiring sanity
// test guards against the registry-drift root cause; this test pins
// the runtime behavior.
func TestCloudAssetEnricher_UnregisteredTypeErrors(t *testing.T) {
	t.Parallel()
	fetch := func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
		return map[string]any{"name": "x"}, nil
	}
	e := newCloudAssetEnricher("google_synthetic_unregistered_type_xyz", "synthetic.googleapis.com/Thing", fetch)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_synthetic_unregistered_type_xyz",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{"asset_name": "//synthetic.googleapis.com/projects/p/things/x"},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "google_synthetic_unregistered_type_xyz")
}

// TestCloudAssetEnricher_Enrich_Normalized exercises the #510
// Normalizer hook on the wired types (google_compute_firewall,
// google_compute_instance). For each type the test feeds a fake CAI
// response with the known-divergent fields (self-link URL on
// `network`, `tags: {items: [...]}` wrapper) and asserts the
// post-Normalizer Layer-1 payload lands the values on the bare TF
// field names (`network` short name, flat `tags` list).
func TestCloudAssetEnricher_Enrich_Normalized(t *testing.T) {
	t.Parallel()

	t.Run("google_compute_firewall", func(t *testing.T) {
		t.Parallel()
		fetch := func(_ context.Context, _, _, _ string) (map[string]any, error) {
			return map[string]any{
				"name":        "allow-ssh",
				"network":     "https://www.googleapis.com/compute/v1/projects/real-proj/global/networks/io-test-net",
				"description": "ssh",
				"direction":   "INGREST",
				"priority":    1000.0,
				"sourceRanges": []any{
					"0.0.0.0/0",
				},
			}, nil
		}
		// Pull the Normalizer from the live cloudAssetTypeConfigs
		// registration so this test catches drift in the per-type
		// wiring (chain order, helper choice).
		n := normalizerForAssetType(t, "compute.googleapis.com/Firewall")
		e := newCloudAssetEnricherWithNormalizer("google_compute_firewall", "compute.googleapis.com/Firewall", fetch, n)
		ir := &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:      "google_compute_firewall",
				ProjectID: "real-proj",
				NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/real-proj/global/firewalls/allow-ssh"},
			},
		}
		require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{}))

		decoded, err := generated.UnmarshalAttrs("google_compute_firewall", ir.Attrs)
		require.NoError(t, err)
		fw, ok := decoded.(*generated.GoogleComputeFirewall)
		require.True(t, ok, "decoded type is %T", decoded)

		// network self-link collapsed to bare name.
		require.NotNil(t, fw.Network)
		require.NotNil(t, fw.Network.Literal)
		assert.Equal(t, "io-test-net", *fw.Network.Literal)
		// Untouched fields still flow through the renamer.
		require.NotNil(t, fw.Name)
		require.NotNil(t, fw.Name.Literal)
		assert.Equal(t, "allow-ssh", *fw.Name.Literal)
	})

	t.Run("google_compute_instance", func(t *testing.T) {
		t.Parallel()
		fetch := func(_ context.Context, _, _, _ string) (map[string]any, error) {
			return map[string]any{
				"name":        "io-test-inst",
				"machineType": "https://www.googleapis.com/compute/v1/projects/real-proj/zones/us-east1-b/machineTypes/n1-standard-1",
				"zone":        "https://www.googleapis.com/compute/v1/projects/real-proj/zones/us-east1-b",
				"description": "test",
				"tags": map[string]any{
					"items":       []any{"web", "ssh"},
					"fingerprint": "abc",
				},
				"resourcePolicies": []any{
					"projects/real-proj/regions/us-east1/resourcePolicies/p1",
					"projects/real-proj/regions/us-east1/resourcePolicies/p2",
				},
			}, nil
		}
		n := normalizerForAssetType(t, "compute.googleapis.com/Instance")
		e := newCloudAssetEnricherWithNormalizer("google_compute_instance", "compute.googleapis.com/Instance", fetch, n)
		ir := &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:      "google_compute_instance",
				ProjectID: "real-proj",
				NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/real-proj/zones/us-east1-b/instances/io-test-inst"},
			},
		}
		require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{}))

		decoded, err := generated.UnmarshalAttrs("google_compute_instance", ir.Attrs)
		require.NoError(t, err)
		inst, ok := decoded.(*generated.GoogleComputeInstance)
		require.True(t, ok, "decoded type is %T", decoded)

		// Self-link fields collapsed to short names.
		require.NotNil(t, inst.MachineType)
		require.NotNil(t, inst.MachineType.Literal)
		assert.Equal(t, "n1-standard-1", *inst.MachineType.Literal)
		require.NotNil(t, inst.Zone)
		require.NotNil(t, inst.Zone.Literal)
		assert.Equal(t, "us-east1-b", *inst.Zone.Literal)
		// resourcePolicies list elements collapsed to short names.
		require.NotNil(t, inst.ResourcePolicies)
		require.Len(t, inst.ResourcePolicies, 2)
		require.NotNil(t, inst.ResourcePolicies[0].Literal)
		assert.Equal(t, "p1", *inst.ResourcePolicies[0].Literal)
		require.NotNil(t, inst.ResourcePolicies[1].Literal)
		assert.Equal(t, "p2", *inst.ResourcePolicies[1].Literal)
		// Network tags wrapper flattened to bare list.
		require.NotNil(t, inst.Tags)
		require.Len(t, inst.Tags, 2)
		require.NotNil(t, inst.Tags[0].Literal)
		assert.Equal(t, "web", *inst.Tags[0].Literal)
		require.NotNil(t, inst.Tags[1].Literal)
		assert.Equal(t, "ssh", *inst.Tags[1].Literal)
	})
}

// normalizerForAssetType returns the Normalizer registered on the
// cloudAssetTypeConfigs entry for the given CAI asset type. Test-only;
// fails the test if the type is not registered or has no Normalizer.
// Pulling the Normalizer from the live registration (rather than
// re-constructing one in the test) makes the test a regression guard
// for the registration itself.
func normalizerForAssetType(t *testing.T, assetType string) Normalizer {
	t.Helper()
	for _, cfg := range cloudAssetTypeConfigs {
		if cfg.AssetType == assetType {
			require.NotNilf(t, cfg.Normalizer, "no Normalizer registered for %s", assetType)
			return cfg.Normalizer
		}
	}
	t.Fatalf("no cloudAssetTypeConfigs entry for %s", assetType)
	return nil
}

// TestCloudAssetEnricher_Enrich_NormalizerError pins the failure path:
// a Normalizer that returns an error fails the fetch with the original
// error wrapped, so soft-fail dispatchers can distinguish a
// shape-transform bug from a real CAI API error. Mirrors the AWS
// TestCloudControlEnricher_Enrich_NormalizerError shape.
func TestCloudAssetEnricher_Enrich_NormalizerError(t *testing.T) {
	t.Parallel()
	fetch := func(_ context.Context, _, _, _ string) (map[string]any, error) {
		return map[string]any{"name": "x"}, nil
	}
	boom := errors.New("normalizer-boom")
	n := func(_ json.RawMessage) (json.RawMessage, error) { return nil, boom }
	e := newCloudAssetEnricherWithNormalizer("google_compute_network", "compute.googleapis.com/Network", fetch, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_network",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/real-proj/global/networks/x"},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "normalize compute.googleapis.com/Network")
}

// TestCloudAssetEnricher_NilIRErrors pins the nil-IR guard.
func TestCloudAssetEnricher_NilIRErrors(t *testing.T) {
	t.Parallel()
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", nil)
	err := e.Enrich(context.Background(), nil, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ir is nil")
}

// TestCloudAssetEnricher_NilIdentityErrors pins the
// nil-identity guard on the EnrichByID path.
func TestCloudAssetEnricher_NilIdentityErrors(t *testing.T) {
	t.Parallel()
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", nil)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

// TestCloudAssetEnricher_ResourceTypeMatchesConfig pins the
// ResourceType() return value against its config. A copy-paste bug
// where the enricher's tfType field diverges from the
// cloudAssetTypeConfigs entry's TFType would silently mis-dispatch
// every Enrich call.
func TestCloudAssetEnricher_ResourceTypeMatchesConfig(t *testing.T) {
	t.Parallel()
	e := newCloudAssetEnricher("google_compute_instance", "compute.googleapis.com/Instance", nil)
	assert.Equal(t, "google_compute_instance", e.ResourceType())
}

// TestCloudAssetEnricher_ProductionRegistrationViaEnrichClients pins
// the production wiring path: when a test passes a CloudAsset getter
// via EnrichClients (rather than the constructor's fetch field), the
// enricher resolves it through EnrichClients.CloudAsset.
func TestCloudAssetEnricher_ProductionRegistrationViaEnrichClients(t *testing.T) {
	t.Parallel()
	calls := 0
	getter := &fakeAssetGetter{
		getByName: func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
			calls++
			return map[string]any{"name": "x"}, nil
		},
	}
	e := newCloudAssetEnricher("google_compute_network", "compute.googleapis.com/Network", nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_network",
			ProjectID: "real-proj",
			NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/real-proj/global/networks/x"},
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{CloudAsset: getter})
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "production path must route through EnrichClients.CloudAsset.GetByName")
}

// fakeAssetGetter is a closure-backed gcpAssetGetter for tests.
type fakeAssetGetter struct {
	getByName func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error)
}

func (f *fakeAssetGetter) GetByName(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
	return f.getByName(ctx, scope, assetType, fullName)
}

// Compile-time assertion: the fake implements the interface.
var _ gcpAssetGetter = (*fakeAssetGetter)(nil)

// =====================================================================
// Wiring sanity tests
// =====================================================================

// TestCloudAssetEnricherCoversEveryCAIRoutedType pins that every TF
// type in cloudAssetTypeConfigs has a registered enricher in
// production after NewGCPDiscoverer runs. A drift here (e.g. adding
// a config entry without rebuilding the wiring loop, or accidentally
// breaking the iteration) would silently regress CAI HYBRID
// coverage. Mirrors the AWS counterpart in #490 / #495.
func TestCloudAssetEnricherCoversEveryCAIRoutedType(t *testing.T) {
	t.Parallel()
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	for _, cfg := range cloudAssetTypeConfigs {
		if cfg.Skip {
			continue
		}
		_, ok := d.byTypeEnricher[cfg.TFType]
		assert.True(t, ok, "CAI config entry %s has no registered enricher (wiring loop drift?)", cfg.TFType)
	}
}

// TestCloudAssetEnricherSkipsHandRolledOverrides pins that the
// HYBRID iteration order is right: for every CAI config entry whose
// TFType ALSO has a hand-rolled enricher, the hand-rolled one wins
// (the registered enricher must NOT be a *cloudAssetEnricher).
// Regression-guards a future map-initialization order change that
// would silently flip overrides.
func TestCloudAssetEnricherSkipsHandRolledOverrides(t *testing.T) {
	t.Parallel()
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	handRolled := []string{
		"google_compute_address",
		"google_compute_firewall",
		"google_compute_network",
		"google_pubsub_subscription",
		"google_pubsub_topic",
		"google_secret_manager_secret",
		"google_storage_bucket",
	}
	for _, tfType := range handRolled {
		enr, ok := d.byTypeEnricher[tfType]
		require.True(t, ok, "%s must be registered", tfType)
		_, isCAI := enr.(*cloudAssetEnricher)
		assert.False(t, isCAI, "%s: hand-rolled enricher must win over CAI HYBRID fallback", tfType)
	}
}

// TestCloudAssetTypeConfigs_UniqueTFTypes pins that no TF type
// appears twice in the config. A duplicate would silently overwrite
// the earlier entry in the wiring loop — caught here as a config
// hygiene check.
func TestCloudAssetTypeConfigs_UniqueTFTypes(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, len(cloudAssetTypeConfigs))
	for _, cfg := range cloudAssetTypeConfigs {
		if seen[cfg.TFType] {
			t.Errorf("duplicate TFType in cloudAssetTypeConfigs: %s", cfg.TFType)
		}
		seen[cfg.TFType] = true
	}
}

// TestCloudAssetTypeConfigs_AllAssetTypesNonEmpty pins that every
// entry has an AssetType set. An empty AssetType would cause CAI to
// scan every asset type in the project on every enrichment call —
// performance disaster waiting to happen.
func TestCloudAssetTypeConfigs_AllAssetTypesNonEmpty(t *testing.T) {
	t.Parallel()
	for _, cfg := range cloudAssetTypeConfigs {
		if strings.TrimSpace(cfg.AssetType) == "" {
			t.Errorf("cloudAssetTypeConfigs %s: AssetType must be non-empty", cfg.TFType)
		}
		if strings.TrimSpace(cfg.TFType) == "" {
			t.Errorf("cloudAssetTypeConfigs has entry with empty TFType (AssetType=%q)", cfg.AssetType)
		}
	}
}

// TestCloudAssetTypeConfigs_TFTypesAreRegisteredInGenerated pins
// that every config entry's TF type has a corresponding generated
// Layer-1 struct. A drift here would let an enricher land in the
// production registry but fail at every Enrich call with
// "no registered type" — caught early via this static check.
func TestCloudAssetTypeConfigs_TFTypesAreRegisteredInGenerated(t *testing.T) {
	t.Parallel()
	for _, cfg := range cloudAssetTypeConfigs {
		_, _, ok := generated.Lookup(cfg.TFType)
		assert.True(t, ok, "cloudAssetTypeConfigs entry %s has no generated.Google<Type> struct registered (add the codegen output before adding to the config)", cfg.TFType)
	}
}

// =====================================================================
// JSON-round-trip helper used by the round-trip tests above.
// =====================================================================

// jsonRoundTrip is a tiny helper used by tests that want to confirm
// the enricher's ir.Attrs is decodable into the typed struct and
// re-marshalable into a stable shape. Verified once here so the
// downstream tests stay terse.
//
//nolint:unused // useful as a test helper for future per-type round-trip tests
func jsonRoundTrip(t *testing.T, tfType string, raw json.RawMessage) any {
	t.Helper()
	decoded, err := generated.UnmarshalAttrs(tfType, raw)
	require.NoError(t, err)
	return decoded
}
