package aws

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToSliceOfMaps_EmptyInput_NotNull pins the #255 contract: a successful
// empty round-trip yields []map[string]any{} (non-nil) so the JSON wire
// shape is `[]`, not `null`. This helper is the consolidating fix for the
// Bedrock list-knowledge-bases / list-agents / list-guardrails callers
// that previously returned nil top-level slices and surfaced as the
// reliable UI's "Deploy infrastructure first." fallback.
func TestToSliceOfMaps_EmptyInput_NotNull(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   any
	}{
		{"empty []any", []any{}},
		{"empty []map[string]any", []map[string]any{}},
		{"typed empty []string", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := toSliceOfMaps(tc.in)
			require.NotNil(t, out, "empty input must round-trip to non-nil []")
			assert.Empty(t, out)
			b, err := json.Marshal(out)
			require.NoError(t, err)
			assert.Equal(t, "[]", string(b),
				"toSliceOfMaps must marshal empty result as [] not null (#255)")
		})
	}
}

// TestToSliceOfMaps_NilInput preserves the historical fail-closed shape:
// json.Marshal(nil) -> "null" -> Unmarshal into []map[string]any produces
// nil. The helper signals upstream-error / unmarshalable shape via nil so
// callers can differentiate "no records" (success path, post-#255 returns
// []) from "shape mismatch" (returns nil).
func TestToSliceOfMaps_NilInput(t *testing.T) {
	t.Parallel()
	out := toSliceOfMaps(nil)
	assert.Nil(t, out, "nil input continues to surface as nil (fail-closed)")
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
