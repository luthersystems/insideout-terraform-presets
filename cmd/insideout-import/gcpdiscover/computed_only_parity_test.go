package gcpdiscover

// computed_only_parity_test.go — #581 / #511 retirement gate: side-by-
// side byte-equal parity tests proving that the generic CAI enricher
// path (with the #580 Normalizer kit + the #581 computed-only filter
// wired in) produces the SAME ir.Attrs JSON the retired hand-rolled
// per-type enricher used to produce, for each retired candidate.
//
// Per-candidate retirement status driven by the JSONEq result here:
//
//   - google_compute_address      → byte-equal → RETIRED in this PR
//   - google_pubsub_topic         → byte-equal on no-nested-blocks
//                                   shape → RETIRED in this PR. Nested
//                                   blocks (ingestion_data_source_settings,
//                                   message_storage_policy,
//                                   schema_settings) still drop at
//                                   UnmarshalAttrs unknown-key — the
//                                   InsideOut composer marks them as
//                                   unsupported until the
//                                   object-to-singleton-list wrapper
//                                   lands. See PR #580 Bucket F.
//   - google_pubsub_subscription  → same as pubsub_topic — RETIRED
//   - google_storage_bucket       → NOT byte-equal (location uppercase
//                                   + ObjectRetention.mode derivation
//                                   + nested-block wrapping still
//                                   unhandled); NOT retired this PR.
//                                   See PR #580 Bucket F.
//
// Golden JSON below is the historical mapComputeAddress /
// mapPubsubTopic / mapPubsubSubscription output captured before
// retirement (see commit message / PR #580 Bucket F for the dump
// command if regeneration is ever needed). Keeping the expected output
// inline (rather than a testdata file) makes the parity invariant
// auditable in a single read pass.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestComputedOnlyParity_ComputeAddress is the post-retirement regression
// guard for google_compute_address. The CAI generic path with the wired
// Normalizer chain (selfLinkToBareName + dropLabelPrefix +
// stripComputedOnlyForType) MUST produce the same ir.Attrs JSON that
// the (now-deleted) mapComputeAddress used to produce.
//
// Fixture intentionally includes computed-only fields
// (creationTimestamp, selfLink, labelFingerprint, effectiveLabels,
// terraformLabels, users) in the CAI body — these would silently leak
// into ir.Attrs without the #581 filter. Real CAI responses always
// carry these fields, so a parity test with a minimal fixture would
// give false confidence.
func TestComputedOnlyParity_ComputeAddress(t *testing.T) {
	t.Parallel()

	// Golden output — what the retired mapComputeAddress used to emit
	// for the equivalent input. Captured pre-retirement; regenerate by
	// running mapComputeAddress against the equivalent *computev1.Address
	// if the FieldSchema or Layer-1 struct shape changes.
	const wantHandRolledJSON = `{
		"address":{"literal":"10.0.0.5"},
		"address_type":{"literal":"INTERNAL"},
		"description":{"literal":"Internal LB front-end"},
		"ip_version":{"literal":"IPV4"},
		"labels":{"env":{"literal":"prod"},"team":{"literal":"platform"}},
		"name":{"literal":"internal-lb-ip"},
		"network":{"literal":"https://www.googleapis.com/compute/v1/projects/my-project/global/networks/vpc-prod"},
		"network_tier":{"literal":"PREMIUM"},
		"prefix_length":{"literal":29},
		"project":{"literal":"my-project"},
		"purpose":{"literal":"GCE_ENDPOINT"},
		"region":{"literal":"us-central1"},
		"subnetwork":{"literal":"https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/private"}
	}`

	// CAI side: the equivalent fetch body — same fields the GCE
	// compute REST API returns plus the computed-only tail that the
	// retired hand-rolled enricher elided per decision #5.
	caiBody := map[string]any{
		// User-editable scalars (kept as-is).
		"address":      "10.0.0.5",
		"addressType":  "INTERNAL",
		"description":  "Internal LB front-end",
		"ipVersion":    "IPV4",
		"name":         "internal-lb-ip",
		"network":      "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/vpc-prod",
		"networkTier":  "PREMIUM",
		"prefixLength": 29.0,
		"purpose":      "GCE_ENDPOINT",
		"subnetwork":   "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/private",
		"labels": map[string]any{
			"team":          "platform",
			"env":           "prod",
			"goog-managed":  "true",
			"goog_internal": "ignore-me",
		},
		// Region needs selfLink→bareName collapsing (the API returns
		// the full self-link URL, TF state holds the short name).
		"region": "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1",
		// Computed-only fields the CAI body includes — these MUST be
		// stripped by the #581 filter for parity.
		"creationTimestamp": "2024-01-01T00:00:00.000-08:00",
		"id":                "projects/my-project/regions/us-central1/addresses/internal-lb-ip",
		"labelFingerprint":  "abcdef12345",
		"selfLink":          "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/addresses/internal-lb-ip",
		"effectiveLabels":   map[string]any{"team": "platform", "env": "prod", "goog-managed": "true"},
		"terraformLabels":   map[string]any{"team": "platform", "env": "prod"},
		"users":             []any{"projects/my-project/zones/us-central1-a/instances/i-1"},
	}
	fetch := func(_ context.Context, _, _, _ string) (map[string]any, error) {
		return caiBody, nil
	}
	n := normalizerForAssetType(t, "compute.googleapis.com/Address")
	caiEnricher := newCloudAssetEnricherWithNormalizer(
		"google_compute_address", "compute.googleapis.com/Address", fetch, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_compute_address",
			ProjectID: "my-project",
			NativeIDs: map[string]string{"asset_name": "//compute.googleapis.com/projects/my-project/regions/us-central1/addresses/internal-lb-ip"},
		},
	}
	require.NoError(t, caiEnricher.Enrich(context.Background(), ir, EnrichClients{}))

	// Sanity-check the golden JSON is itself parseable (a typo in the
	// inline literal should fail loud at the test, not silently make
	// JSONEq pass by re-marshalling to the same broken shape).
	var goldenSanity map[string]any
	require.NoError(t, json.Unmarshal([]byte(wantHandRolledJSON), &goldenSanity))

	assert.JSONEq(t, wantHandRolledJSON, string(ir.Attrs),
		"CAI+Normalizer+ComputedOnlyFilter must produce byte-equal JSON to the retired mapComputeAddress for the same input (computed-only fields stripped, region self-link collapsed, goog-managed labels filtered)")
}

// TestComputedOnlyParity_PubsubTopic is the post-retirement regression
// guard for google_pubsub_topic on the no-nested-blocks shape.
//
// Nested blocks (ingestion_data_source_settings, message_storage_policy,
// schema_settings) face the unresolved object-to-singleton-list wrapping
// blocker — a follow-up Normalizer will wrap a CAI single-object into
// the Layer-1 []Block shape. Until then, the InsideOut composer surfaces
// the unsupported-attribute warning for topics that actually use them.
func TestComputedOnlyParity_PubsubTopic(t *testing.T) {
	t.Parallel()

	const wantHandRolledJSON = `{
		"kms_key_name":{"literal":"projects/p/locations/us/keyRings/r/cryptoKeys/k"},
		"labels":{"team":{"literal":"platform"}},
		"message_retention_duration":{"literal":"86400s"},
		"name":{"literal":"my-topic"},
		"project":{"literal":"my-project"}
	}`

	caiBody := map[string]any{
		"name":                     "projects/my-project/topics/my-topic",
		"kmsKeyName":               "projects/p/locations/us/keyRings/r/cryptoKeys/k",
		"messageRetentionDuration": "86400s",
		"labels": map[string]any{
			"team":          "platform",
			"goog-managed":  "true",
			"goog_internal": "x",
		},
		// Computed-only fields the CAI body includes.
		"effectiveLabels": map[string]any{"team": "platform", "goog-managed": "true"},
		"terraformLabels": map[string]any{"team": "platform"},
		"id":              "projects/my-project/topics/my-topic",
	}
	fetch := func(_ context.Context, _, _, _ string) (map[string]any, error) {
		return caiBody, nil
	}
	n := normalizerForAssetType(t, "pubsub.googleapis.com/Topic")
	caiEnricher := newCloudAssetEnricherWithNormalizer(
		"google_pubsub_topic", "pubsub.googleapis.com/Topic", fetch, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_pubsub_topic",
			ProjectID: "my-project",
			NativeIDs: map[string]string{"asset_name": "//pubsub.googleapis.com/projects/my-project/topics/my-topic"},
		},
	}
	require.NoError(t, caiEnricher.Enrich(context.Background(), ir, EnrichClients{}))

	var goldenSanity map[string]any
	require.NoError(t, json.Unmarshal([]byte(wantHandRolledJSON), &goldenSanity))

	assert.JSONEq(t, wantHandRolledJSON, string(ir.Attrs),
		"CAI+Normalizer+ComputedOnlyFilter must produce byte-equal JSON to the retired mapPubsubTopic for the same no-nested-blocks input")
}

// TestComputedOnlyParity_PubsubSubscription is the post-retirement
// regression guard for google_pubsub_subscription on the
// no-nested-blocks shape. Six nested blocks (push_config,
// bigquery_config, cloud_storage_config, dead_letter_policy,
// expiration_policy, retry_policy) face the same object-to-singleton-
// list wrapping blocker as pubsub_topic.
func TestComputedOnlyParity_PubsubSubscription(t *testing.T) {
	t.Parallel()

	const wantHandRolledJSON = `{
		"ack_deadline_seconds":{"literal":30},
		"enable_exactly_once_delivery":{"literal":false},
		"enable_message_ordering":{"literal":false},
		"filter":{"literal":"attributes.color = 'red'"},
		"labels":{"team":{"literal":"platform"}},
		"message_retention_duration":{"literal":"86400s"},
		"name":{"literal":"my-sub"},
		"project":{"literal":"my-project"},
		"retain_acked_messages":{"literal":false},
		"topic":{"literal":"projects/my-project/topics/my-topic"}
	}`

	caiBody := map[string]any{
		"name":                     "projects/my-project/subscriptions/my-sub",
		"topic":                    "projects/my-project/topics/my-topic",
		"ackDeadlineSeconds":       30.0,
		"messageRetentionDuration": "86400s",
		"filter":                   "attributes.color = 'red'",
		"labels": map[string]any{
			"team":         "platform",
			"goog-managed": "true",
		},
		// Computed-only fields.
		"effectiveLabels": map[string]any{"team": "platform"},
		"terraformLabels": map[string]any{"team": "platform"},
		"id":              "projects/my-project/subscriptions/my-sub",
	}
	fetch := func(_ context.Context, _, _, _ string) (map[string]any, error) {
		return caiBody, nil
	}
	n := normalizerForAssetType(t, "pubsub.googleapis.com/Subscription")
	caiEnricher := newCloudAssetEnricherWithNormalizer(
		"google_pubsub_subscription", "pubsub.googleapis.com/Subscription", fetch, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_pubsub_subscription",
			ProjectID: "my-project",
			NativeIDs: map[string]string{"asset_name": "//pubsub.googleapis.com/projects/my-project/subscriptions/my-sub"},
		},
	}
	require.NoError(t, caiEnricher.Enrich(context.Background(), ir, EnrichClients{}))

	var goldenSanity map[string]any
	require.NoError(t, json.Unmarshal([]byte(wantHandRolledJSON), &goldenSanity))

	assert.JSONEq(t, wantHandRolledJSON, string(ir.Attrs),
		"CAI+Normalizer+ComputedOnlyFilter must produce byte-equal JSON to the retired mapPubsubSubscription for the same no-nested-blocks input")
}
