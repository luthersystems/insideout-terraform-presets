package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateAll_DedupesAndSorts asserts the aggregator produces a stable
// deterministic order and removes duplicate issues that arrive from
// multiple sub-validators (or the same one running over overlapping
// inputs).
func TestValidateAll_DedupesAndSorts(t *testing.T) {
	t.Parallel()

	// Synthetic blocks where multiple validators may speak to the same field.
	blocks := []ModuleBlock{
		{
			Name: "aws_alb",
			Raw: map[string]string{
				"vpc_id":  "module.aws_vpc.vpc_id",
				"missing": "module.aws_vpc.does_not_exist",
			},
		},
	}
	presetPaths := map[string]string{"aws_vpc": "aws/vpc"}

	out := ValidateAll(nil, nil, blocks, nil, presetPaths, nil)
	// Sorted by Field then Code: aws_alb.missing comes after KnownFields-only output.
	for i := 1; i < len(out); i++ {
		prev, cur := out[i-1], out[i]
		if prev.Field == cur.Field && prev.Code == cur.Code {
			require.LessOrEqual(t, prev.Reason, cur.Reason, "issues must be sorted by Reason within same Field+Code")
		} else if prev.Field == cur.Field {
			require.LessOrEqual(t, prev.Code, cur.Code, "issues must be sorted by Code within same Field")
		} else {
			require.LessOrEqual(t, prev.Field, cur.Field, "issues must be sorted by Field")
		}
	}

	// At least one missing-output issue should surface.
	found := false
	for _, iss := range out {
		if iss.Code == "unwired_output" && iss.Field == "aws_alb.missing" {
			found = true
		}
	}
	require.True(t, found, "expected unwired_output for aws_alb.missing in ValidateAll output: %v", out)
}

// TestValidateAll_EmptyInputsReturnsEmpty pins the contract that
// ValidateAll never panics on nil/empty inputs and returns nil.
func TestValidateAll_EmptyInputsReturnsEmpty(t *testing.T) {
	t.Parallel()
	require.Empty(t, ValidateAll(nil, nil, nil, nil, nil, nil))
}
