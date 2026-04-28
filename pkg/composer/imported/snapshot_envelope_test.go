package imported

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotEnvelope_RoundTrip locks in the issue #144 acceptance criterion:
// a synthetic stack-snapshot JSON envelope carrying both the existing preset
// shape (components, config, pricing — modeled here as opaque RawMessage) and
// a populated imported list round-trips byte-identically.
//
// The real envelope is owned by the reliable repo's stack_versions row; this
// test does not attempt to mirror it field-by-field. It only proves that
// adding "imported" alongside the existing keys does not lose semantic data.
func TestSnapshotEnvelope_RoundTrip(t *testing.T) {
	t.Parallel()

	resources := []ImportedResource{
		{
			Identity: ResourceIdentity{
				Cloud:           "aws",
				Type:            "aws_sqs_queue",
				Address:         "aws_sqs_queue.orders_dlq",
				NameHint:        "orders-DLQ",
				ProviderConfig:  "aws.imported",
				ProviderSource:  "registry.terraform.io/hashicorp/aws",
				ProviderVersion: "6.7.0",
				SchemaVersion:   "v1",
				AccountID:       "123456789012",
				Region:          "us-east-1",
				ImportID:        "arn:aws:sqs:us-east-1:123456789012:orders-DLQ",
				NativeIDs: map[string]string{
					"arn":  "arn:aws:sqs:us-east-1:123456789012:orders-DLQ",
					"name": "orders-DLQ",
				},
			},
			Tier:   TierImportedFlat,
			Source: SourceImporter,
			Attributes: map[string]any{
				"name":       "orders-DLQ",
				"fifo_queue": false,
			},
			FieldEdits: map[string]FieldEdit{
				"visibility_timeout_seconds": {
					Source:   SourceRiley,
					EditedAt: time.Date(2026, 4, 27, 14, 30, 0, 0, time.UTC),
					OldValue: float64(30),
					NewValue: float64(60),
				},
			},
		},
		{
			Identity: ResourceIdentity{
				Cloud:           "gcp",
				Type:            "google_storage_bucket",
				Address:         "google_storage_bucket.assets",
				NameHint:        "assets",
				ProviderConfig:  "google.imported",
				ProviderSource:  "registry.terraform.io/hashicorp/google",
				ProviderVersion: "5.40.0",
				ProjectID:       "my-project",
				Location:        "US",
				ImportID:        "my-project/assets",
			},
			Tier:   TierImportedConformant,
			Source: SourceImporter,
		},
		{
			Identity: ResourceIdentity{
				Cloud:   "aws",
				Type:    "aws_kms_key",
				Address: "aws_kms_key.legacy",
			},
			Tier:   TierImportedMissing,
			Source: SourceInspector,
		},
	}

	envelope := map[string]json.RawMessage{
		"components": json.RawMessage(`{"cloud":"AWS","aws_vpc":"Private"}`),
		"config":     json.RawMessage(`{"environment":"staging"}`),
		"pricing":    json.RawMessage(`{"monthly_total":42.5}`),
		"imported":   mustMarshal(t, resources),
	}

	first, err := json.Marshal(envelope)
	require.NoError(t, err)

	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(first, &got))

	second, err := json.Marshal(got)
	require.NoError(t, err)
	// Byte-identical round trip: encoding/json sorts map keys alphabetically
	// on both passes, so any drift indicates a real serialization bug
	// (re-ordered struct fields, hand-rolled MarshalJSON misbehavior, etc.).
	require.Equal(t, string(first), string(second),
		"envelope must round-trip byte-identically")

	// And the imported slice itself must round-trip back to the same Go
	// value.
	var rt []ImportedResource
	require.NoError(t, json.Unmarshal(got["imported"], &rt))
	require.Len(t, rt, len(resources))
	for i, want := range resources {
		assert.Truef(t, equalIdentity(want.Identity, rt[i].Identity),
			"identity[%d] mismatch", i)
		assert.Equal(t, want.Tier, rt[i].Tier)
		assert.Equal(t, want.Source, rt[i].Source)
		assert.Equal(t, want.Attributes, rt[i].Attributes)
	}
}

// equalIdentity compares two identities without relying on map ordering.
func equalIdentity(a, b ResourceIdentity) bool {
	if a.Cloud != b.Cloud || a.Type != b.Type || a.Address != b.Address ||
		a.NameHint != b.NameHint || a.ProviderConfig != b.ProviderConfig ||
		a.ProviderSource != b.ProviderSource || a.ProviderVersion != b.ProviderVersion ||
		a.SchemaVersion != b.SchemaVersion || a.AccountID != b.AccountID ||
		a.ProjectID != b.ProjectID || a.Region != b.Region ||
		a.Location != b.Location || a.ImportID != b.ImportID {
		return false
	}
	return mapsEqual(a.ProviderIdentity, b.ProviderIdentity) &&
		mapsEqual(a.NativeIDs, b.NativeIDs)
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
