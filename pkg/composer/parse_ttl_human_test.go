package composer

import "testing"

// TestParseTTLSeconds_HumanWordForms locks the fix for luthersystems/reliable#1994:
// parseTTLSeconds must accept the human duration word forms humanizeDuration
// emits (e.g. SQS visibilityTimeout "30 seconds"), not only "<N>s/m/h" and
// "<N>day(s)". Existing accepted forms must keep working.
func TestParseTTLSeconds_HumanWordForms(t *testing.T) {
	ok := []struct {
		in   string
		want int
	}{
		// New: human word forms (what humanizeDuration produces).
		{"30 seconds", 30},
		{"1 second", 1},
		{"10 minutes", 600},
		{"1 minute", 60},
		{"1 hour", 3600},
		{"5 hours", 18000},
		{"30 Seconds", 30},   // case-insensitive
		{" 30 seconds ", 30}, // surrounding whitespace
		// Existing forms still accepted.
		{"0", 0},
		{"600", 600},
		{"30s", 30},
		{"10m", 600},
		{"1h", 3600},
		{"1day", 86400},
		{"7 days", 604800},
	}
	for _, tc := range ok {
		got, err := parseTTLSeconds(tc.in, "visibilityTimeout")
		if err != nil {
			t.Errorf("parseTTLSeconds(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseTTLSeconds(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}

	bad := []string{"potato", "soon", "", "-5 minutes"}
	for _, in := range bad {
		if _, err := parseTTLSeconds(in, "visibilityTimeout"); err == nil {
			t.Errorf("parseTTLSeconds(%q) = nil error, want validation error", in)
		}
	}
}

// TestParseTTLSeconds_RoundTripsHumanizeDuration is the symmetry guard: every
// machine duration the composer humanizes for display must parse back to the
// same number of seconds. This keeps parseTTLSeconds and humanizeDuration from
// drifting apart again (#1994).
func TestParseTTLSeconds_RoundTripsHumanizeDuration(t *testing.T) {
	cases := []struct {
		machine string
		want    int
	}{
		{"30s", 30},
		{"10m", 600},
		{"1h", 3600},
		{"1s", 1},
		{"1m", 60},
		{"2h", 7200},
	}
	for _, tc := range cases {
		human := humanizeDuration(tc.machine)
		got, err := parseTTLSeconds(human, "visibilityTimeout")
		if err != nil {
			t.Errorf("parseTTLSeconds(humanizeDuration(%q)=%q) error: %v", tc.machine, human, err)
			continue
		}
		if got != tc.want {
			t.Errorf("round-trip %q -> humanize %q -> parse %d, want %d", tc.machine, human, got, tc.want)
		}
	}
}
