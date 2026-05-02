package observability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDidYouMean(t *testing.T) {
	t.Parallel()
	valid := []string{"describe-instances", "describe-vpcs", "describe-subnets", "get-metrics"}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Distance-1 boundary (must match) — single-character delete.
		{"distance_1_drop_trailing_s", "describe-instance", "describe-instances"},
		{"distance_1_typo_get_metric", "get-metric", "get-metrics"},
		// Distance-2 (must match) — adjacent transposition.
		{"distance_2_transposition", "descirbe-instances", "describe-instances"},
		// Distance-3 boundary (must match). `get-metr` → `get-metrics`
		// requires inserting "i", "c", "s": exactly 3 edits, all other
		// candidates are far further.
		{"distance_3_below_threshold", "get-metr", "get-metrics"},
		// Distance-4 boundary (must reject). `get-met` → `get-metrics`
		// requires 4 inserts; all other candidates are >> 4. The
		// helper's `bestDist := 4; d < bestDist` guard must reject.
		{"distance_4_above_threshold", "get-met", ""},
		// Far input: must reject regardless of length.
		{"far_input_rejects", "completely-wrong-action", ""},
		// Empty input: trivially d == len(candidate) for every
		// candidate, so all are >> 3 — must reject.
		{"empty_input_rejects", "", ""},
		// d == 0 self-match: the helper MUST NOT suggest the input
		// back at itself (#227 — confusing tell of an internal
		// dispatch routing bug, not a typo).
		{"exact_match_d0_no_self_suggest", "describe-instances", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := didYouMean(tt.input, valid)
			assert.Equal(t, tt.want, got, "didYouMean(%q, ...)", tt.input)
		})
	}

	t.Run("nil_valid_returns_empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", didYouMean("foo", nil))
	})
}

func TestUnsupportedActionError(t *testing.T) {
	t.Parallel()
	validActions := []string{"describe-instances", "describe-vpcs", "get-metrics"}

	// Golden full-string assertions: this string is the wire contract
	// round-tripped to the LLM as a tool-result envelope. Substring
	// checks miss byte-level mutations (drop `?`, drop parens, reorder
	// hint vs supported list, truncate the list-actions pointer to a
	// bare token). Lock the literal here.
	t.Run("golden_with_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedActionError("EC2", "describe-instance", validActions)
		require.Error(t, err)
		want := `unsupported EC2 action: "describe-instance" (did you mean "describe-instances"?). Supported actions: describe-instances, describe-vpcs, get-metrics. Use action "list-actions" to see all supported actions for a service.`
		assert.Equal(t, want, err.Error())
	})

	t.Run("golden_without_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedActionError("EC2", "zzzzz-totally-wrong", validActions)
		require.Error(t, err)
		want := `unsupported EC2 action: "zzzzz-totally-wrong". Supported actions: describe-instances, describe-vpcs, get-metrics. Use action "list-actions" to see all supported actions for a service.`
		assert.Equal(t, want, err.Error())
	})

	t.Run("golden_empty_actions", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedActionError("EC2", "foo", nil)
		require.Error(t, err)
		want := `unsupported EC2 action: "foo". Use action "list-actions" to see all supported actions for a service.`
		assert.Equal(t, want, err.Error())
	})

	// Public-API exercise of the d>0 self-suggest guard: feeding an
	// exact-match action through the user-facing builder must NOT
	// produce a `(did you mean "<same>"?)` hint. A regression dropping
	// the d>0 guard surfaces here as an unexpected hint substring.
	t.Run("exact_match_no_self_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedActionError("EC2", "describe-instances", validActions)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "did you mean")
	})
}

func TestUnsupportedServiceError(t *testing.T) {
	t.Parallel()
	validServices := []string{"ec2", "rds", "s3", "vpc"}

	t.Run("golden_with_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedServiceError("ec3", validServices)
		require.Error(t, err)
		// Note: the service builder does NOT append a trailing period
		// after the supported-services list (reliable's reference
		// behavior — pin it here so a future "tidiness" tweak doesn't
		// silently change the wire format).
		want := `unsupported service: "ec3" (did you mean "ec2"?). Supported services: ec2, rds, s3, vpc`
		assert.Equal(t, want, err.Error())
	})

	t.Run("golden_without_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedServiceError("zzznotaservice", validServices)
		require.Error(t, err)
		want := `unsupported service: "zzznotaservice". Supported services: ec2, rds, s3, vpc`
		assert.Equal(t, want, err.Error())
	})

	t.Run("golden_empty_services", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedServiceError("foo", nil)
		require.Error(t, err)
		want := `unsupported service: "foo"`
		assert.Equal(t, want, err.Error())
	})

	// Pass a single-candidate list so the only possible match is the
	// input itself; the d>0 guard must fire and produce no hint. Using
	// the broader validServices set wouldn't isolate the guard because
	// "ec2" is within d=3 of "rds"/"s3"/"vpc" (3-char strings of low
	// alphabet overlap), so a regression dropping `d > 0` would still
	// emit *some* hint via a non-self candidate.
	t.Run("exact_match_no_self_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedServiceError("ec2", []string{"ec2"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "did you mean")
	})

	// Cross-package invariant: pkg/observability/discovery/aws/
	// dispatcher.go::unsupportedServiceError relies on the literal
	// "unsupported service" prefix to dedupe the sentinel + body via
	// strings.TrimPrefix. If this prefix changes, the wrapped AWS
	// sentinel error silently emits a malformed string (sentinel
	// errors.Is keeps working; substring tests keep passing). Pin the
	// prefix here so the cross-package coupling fails loudly instead.
	t.Run("starts_with_canonical_prefix_for_sentinel_wrapping", func(t *testing.T) {
		t.Parallel()
		for _, sample := range []struct {
			service string
			valid   []string
		}{
			{"ec3", validServices},
			{"zzznotaservice", validServices},
			{"foo", nil},
		} {
			err := UnsupportedServiceError(sample.service, sample.valid)
			require.Error(t, err)
			assert.True(t,
				strings.HasPrefix(err.Error(), "unsupported service"),
				"discovery/aws/dispatcher.go::unsupportedServiceError relies on this exact prefix; got: %q",
				err.Error(),
			)
		}
	})
}
