package aws

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToSliceOfMaps_EmptyInput_NotNull pins the #255 contract: empty +
// nil-slice inputs round-trip to []map[string]any{} (non-nil) so the JSON
// wire shape is `[]`, not `null`. AWS SDK V2 list responses commonly
// expose empty results as typed-nil slices (e.g.
// `bedrockagent.ListKnowledgeBasesOutput.KnowledgeBaseSummaries == nil`)
// — the post-Unmarshal restoration in toSliceOfMaps catches that case.
// This helper is the consolidating fix for the Bedrock
// list-knowledge-bases / list-agents / list-guardrails callers that
// previously returned nil top-level slices and surfaced as the reliable
// UI's "Deploy infrastructure first." fallback.
func TestToSliceOfMaps_EmptyInput_NotNull(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   any
	}{
		{"untyped nil", nil},
		{"empty []any", []any{}},
		{"empty []map[string]any", []map[string]any{}},
		{"typed empty []string", []string{}},
		{"typed nil []string", []string(nil)},
		{"typed nil []map[string]any", []map[string]any(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := toSliceOfMaps(tc.in)
			require.NotNil(t, out, "empty/nil input must round-trip to non-nil []")
			assert.Empty(t, out)
			b, err := json.Marshal(out)
			require.NoError(t, err)
			assert.Equal(t, "[]", string(b),
				"toSliceOfMaps must marshal empty result as [] not null (#255)")
		})
	}
}

// TestToSliceOfMaps_RoundTripsRecords sanity-checks the success path with
// real records — guards against regressions to the json round-trip.
func TestToSliceOfMaps_RoundTripsRecords(t *testing.T) {
	t.Parallel()
	type rec struct {
		Name string `json:"Name"`
		ARN  string `json:"Arn"`
	}
	in := []rec{{Name: "a", ARN: "arn:a"}, {Name: "b", ARN: "arn:b"}}
	out := toSliceOfMaps(in)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0]["Name"])
	assert.Equal(t, "arn:b", out[1]["Arn"])
}

// TestToSliceOfMaps_UnmarshalFailure_ReturnsNil pins the documented
// fail-closed contract: a value that marshals to a JSON object (not an
// array) cannot be unmarshaled into []map[string]any, and the helper
// surfaces the shape mismatch as nil rather than [] so callers can
// distinguish "no records" from "shape error".
func TestToSliceOfMaps_UnmarshalFailure_ReturnsNil(t *testing.T) {
	t.Parallel()
	out := toSliceOfMaps(struct{ X int }{X: 1})
	assert.Nil(t, out, "shape mismatch must surface as nil (fail-closed)")
}

// TestNilSliceToEmpty pins the #255 Pattern B fix: AWS SDK V2 list-*
// responses commonly populate slice fields with typed-nil on empty
// results. The helper normalizes that nil to []T{} so json.Marshal
// emits `[]` instead of `null`.
func TestNilSliceToEmpty(t *testing.T) {
	t.Parallel()

	t.Run("typed nil []string → non-nil []string{}", func(t *testing.T) {
		t.Parallel()
		var in []string
		got := nilSliceToEmpty(in)
		require.NotNil(t, got)
		assert.Empty(t, got)
		b, err := json.Marshal(got)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(b))
	})
	t.Run("typed nil []int → non-nil []int{}", func(t *testing.T) {
		t.Parallel()
		var in []int
		got := nilSliceToEmpty(in)
		require.NotNil(t, got)
		assert.Empty(t, got)
		b, err := json.Marshal(got)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(b))
	})
	t.Run("non-nil empty slice → returned unchanged", func(t *testing.T) {
		t.Parallel()
		in := []string{}
		got := nilSliceToEmpty(in)
		require.NotNil(t, got)
		assert.Empty(t, got)
	})
	t.Run("non-empty slice → returned unchanged", func(t *testing.T) {
		t.Parallel()
		in := []string{"a", "b"}
		got := nilSliceToEmpty(in)
		assert.Equal(t, []string{"a", "b"}, got)
	})
}
