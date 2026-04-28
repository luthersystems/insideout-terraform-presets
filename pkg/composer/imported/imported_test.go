package imported

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTier_RoundTrip(t *testing.T) {
	t.Parallel()
	tiers := []Tier{
		TierComposerNative,
		TierComposerGraduated,
		TierImportedFlat,
		TierImportedConformant,
		TierImportedMissing,
		TierExternalByPolicy,
		TierExternalUnsupported,
	}
	for _, tier := range tiers {
		t.Run(string(tier), func(t *testing.T) {
			t.Parallel()
			require.True(t, tier.Valid(), "valid tier const must report Valid()=true")
			b, err := json.Marshal(tier)
			require.NoError(t, err)
			assert.Equal(t, `"`+string(tier)+`"`, string(b))

			var got Tier
			require.NoError(t, json.Unmarshal(b, &got))
			assert.Equal(t, tier, got)
		})
	}
}

func TestTier_ValidRejectsUnknown(t *testing.T) {
	t.Parallel()
	for _, v := range []Tier{"", "Composer", "imported_flat", "Unknown"} {
		assert.Falsef(t, v.Valid(), "%q must not Valid()", v)
	}
}

func TestSource_RoundTrip(t *testing.T) {
	t.Parallel()
	sources := []Source{
		SourceComposer, SourceImporter, SourceInspector,
		SourceRiley, SourceAPI, SourceMCP,
	}
	for _, s := range sources {
		t.Run(string(s), func(t *testing.T) {
			t.Parallel()
			require.True(t, s.Valid(), "valid source const must report Valid()=true")
			b, err := json.Marshal(s)
			require.NoError(t, err)
			assert.Equal(t, `"`+string(s)+`"`, string(b))

			var got Source
			require.NoError(t, json.Unmarshal(b, &got))
			assert.Equal(t, s, got)
		})
	}
}

func TestSource_ValidRejectsUnknown(t *testing.T) {
	t.Parallel()
	for _, v := range []Source{"", "RILEY", "user", "unknown"} {
		assert.Falsef(t, v.Valid(), "%q must not Valid()", v)
	}
}

func TestMissingAction_RoundTrip(t *testing.T) {
	t.Parallel()
	actions := []MissingAction{
		ActionRemoveFromInsideOut,
		ActionRecreateFromLastImport,
		ActionReclaimExisting,
	}
	for _, a := range actions {
		t.Run(string(a), func(t *testing.T) {
			t.Parallel()
			require.True(t, a.Valid())
			b, err := json.Marshal(a)
			require.NoError(t, err)
			assert.Equal(t, `"`+string(a)+`"`, string(b))

			var got MissingAction
			require.NoError(t, json.Unmarshal(b, &got))
			assert.Equal(t, a, got)
		})
	}
}

func TestMissingAction_ValidRejectsUnknown(t *testing.T) {
	t.Parallel()
	for _, v := range []MissingAction{"", "remove", "RECREATE_FROM_LAST_IMPORT"} {
		assert.Falsef(t, v.Valid(), "%q must not Valid()", v)
	}
}

func TestResourceIdentity_RoundTrip_Empty(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(ResourceIdentity{})
	require.NoError(t, err)
	assert.Equal(t, "{}", string(b), "zero identity must marshal to {}")

	var got ResourceIdentity
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, ResourceIdentity{}, got)
}

func TestResourceIdentity_RoundTrip_Full(t *testing.T) {
	t.Parallel()
	id := fullIdentity()
	got := mustRoundTripIdentical(t, id)
	assert.Equal(t, id, got)
}

func TestImportedResource_RoundTrip_Full(t *testing.T) {
	t.Parallel()
	r := ImportedResource{
		Identity: fullIdentity(),
		Tier:     TierImportedFlat,
		Source:   SourceImporter,
		Attributes: map[string]any{
			"name":                       "orders-dlq",
			"fifo_queue":                 false,
			"visibility_timeout_seconds": float64(30),
			"tags": map[string]any{
				"managed_by": "external",
			},
		},
		FieldEdits: map[string]FieldEdit{
			"visibility_timeout_seconds": {
				Source:   SourceRiley,
				EditedAt: time.Date(2026, 4, 27, 14, 30, 0, 0, time.UTC),
				OldValue: float64(30),
				NewValue: float64(60),
			},
		},
		GraduationCandidate: &PresetMatch{
			PresetKey:  composer.KeyAWSSQS,
			Confidence: 0.85,
			MovedBlocks: []MovedBlock{
				{From: "aws_sqs_queue.dlq", To: "module.aws_sqs.aws_sqs_queue.dlq"},
			},
			BlockingDeltas: []composer.FieldDiff{
				{Field: "fifo_queue", From: "false", To: "true"},
			},
		},
	}
	got := mustRoundTripIdenticalResource(t, r)
	assert.Equal(t, r, got)
}

// TestOmitEmpty pins the exact JSON shape of zero-or-minimal values for every
// type in this package that uses json:"...,omitempty" tags. Stronger than
// "key X is absent" — any change to which fields are omitempty (including
// accidental removal) shows up as an exact-string diff.
//
// FieldEdit.EditedAt intentionally has no omitempty: a missing edit_at
// timestamp on an audit record would silently lose information, so the zero
// value is preserved on the wire.
func TestOmitEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "ResourceIdentity_zero",
			in:   ResourceIdentity{},
			want: `{}`,
		},
		{
			name: "PresetMatch_zero",
			in:   PresetMatch{},
			want: `{}`,
		},
		{
			name: "FieldEdit_zero",
			in:   FieldEdit{},
			want: `{"edited_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			name: "ImportedResource_minimal",
			in: ImportedResource{
				Identity: ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue"},
				Tier:     TierImportedFlat,
				Source:   SourceImporter,
			},
			want: `{"identity":{"cloud":"aws","type":"aws_sqs_queue"},"tier":"ImportedFlat","source":"importer"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(b))
		})
	}
}

// TestFieldEdit_MarshalJSON_ConvertsToUTC proves that FieldEdit.MarshalJSON
// converts a non-UTC time.Time to UTC before serialization. Without the
// custom MarshalJSON, encoding/json would emit a numeric offset (e.g.
// "-07:00") that violates the RFC3339Nano UTC contract.
func TestFieldEdit_MarshalJSON_ConvertsToUTC(t *testing.T) {
	t.Parallel()
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	// Same instant as 2026-04-27T14:30:15.123456789Z, but expressed in
	// LA local time. The serializer must convert this back to UTC.
	laTime := time.Date(2026, 4, 27, 7, 30, 15, 123_456_789, la)
	fe := FieldEdit{
		Source:   SourceRiley,
		EditedAt: laTime,
		OldValue: "before",
		NewValue: "after",
	}
	b, err := json.Marshal(fe)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"edited_at":"2026-04-27T14:30:15.123456789Z"`,
		"non-UTC EditedAt must be converted to UTC at marshal time; got %s", string(b))
	assert.NotContains(t, string(b), "-07:00",
		"serialized output must not retain the source time zone offset")

	var got FieldEdit
	require.NoError(t, json.Unmarshal(b, &got))
	assert.True(t, got.EditedAt.Equal(fe.EditedAt))
	assert.Equal(t, time.UTC, got.EditedAt.Location(),
		"unmarshalled EditedAt must be in UTC")
	assert.Equal(t, fe.Source, got.Source)
	assert.Equal(t, fe.OldValue, got.OldValue)
	assert.Equal(t, fe.NewValue, got.NewValue)
}

func TestPresetMatch_RoundTrip(t *testing.T) {
	t.Parallel()
	pm := PresetMatch{
		PresetKey:  composer.KeyAWSVPC,
		Confidence: 0.42,
		MovedBlocks: []MovedBlock{
			{From: "aws_vpc.main", To: "module.aws_vpc.aws_vpc.main"},
			{From: "aws_subnet.a", To: "module.aws_vpc.aws_subnet.a"},
		},
		BlockingDeltas: []composer.FieldDiff{
			{Field: "cidr_block", From: "10.0.0.0/16", To: "10.1.0.0/16"},
		},
	}
	b, err := json.Marshal(pm)
	require.NoError(t, err)

	// Cross-package byte assertion: catches an upstream rename of
	// composer.FieldDiff or composer.ComponentKey JSON tags here, in the
	// package that owns the contract, instead of in some downstream
	// consumer's failure stack.
	s := string(b)
	assert.Contains(t, s, `"preset_key":"aws_vpc"`)
	assert.Contains(t, s, `"field":"cidr_block"`)
	assert.Contains(t, s, `"from":"10.0.0.0/16"`)

	var got PresetMatch
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, pm, got)
}

// fullIdentity returns a ResourceIdentity with every field populated. Reused
// by several tests.
func fullIdentity() ResourceIdentity {
	return ResourceIdentity{
		Cloud:           "aws",
		Type:            "aws_sqs_queue",
		Address:         "aws_sqs_queue.orders_dlq",
		NameHint:        "orders-DLQ",
		ProviderConfig:  "aws.imported",
		ProviderSource:  "registry.terraform.io/hashicorp/aws",
		ProviderVersion: "6.7.0",
		SchemaVersion:   "v1",
		AccountID:       "123456789012",
		ProjectID:       "",
		Region:          "us-east-1",
		Location:        "",
		ImportID:        "https://sqs.us-east-1.amazonaws.com/123456789012/orders-DLQ",
		ProviderIdentity: map[string]string{
			"region": "us-east-1",
			"name":   "orders-DLQ",
		},
		NativeIDs: map[string]string{
			"arn":  "arn:aws:sqs:us-east-1:123456789012:orders-DLQ",
			"name": "orders-DLQ",
			"url":  "https://sqs.us-east-1.amazonaws.com/123456789012/orders-DLQ",
		},
	}
}

// mustRoundTripIdentical marshals v, asserts a second marshal of the
// unmarshalled value produces byte-identical output, and returns the
// unmarshalled value.
func mustRoundTripIdentical(t *testing.T, v ResourceIdentity) ResourceIdentity {
	t.Helper()
	first, err := json.Marshal(v)
	require.NoError(t, err)

	var got ResourceIdentity
	require.NoError(t, json.Unmarshal(first, &got))

	second, err := json.Marshal(got)
	require.NoError(t, err)
	require.Equalf(t, string(first), string(second),
		"identity must round-trip byte-identically; first=%s second=%s",
		first, second)
	require.False(t, strings.Contains(string(first), `"":`),
		"empty JSON keys must not appear: %s", first)
	return got
}

func mustRoundTripIdenticalResource(t *testing.T, v ImportedResource) ImportedResource {
	t.Helper()
	first, err := json.Marshal(v)
	require.NoError(t, err)

	var got ImportedResource
	require.NoError(t, json.Unmarshal(first, &got))

	second, err := json.Marshal(got)
	require.NoError(t, err)
	require.Equalf(t, string(first), string(second),
		"resource must round-trip byte-identically; first=%s second=%s",
		first, second)
	return got
}
