package awsdiscover

import "testing"

// TestTagSelector_Matches_CaseSensitiveEquality pins both legs of the
// match contract: the same-key/same-value pair matches; differing
// case fails (the asset stores tag values verbatim and the reliable
// wizard's `key=value` UX implies case-sensitive equality).
func TestTagSelector_Matches_CaseSensitiveEquality(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		tags     map[string]string
		selector TagSelector
		want     bool
	}{
		{name: "exact match", tags: map[string]string{"env": "prod"}, selector: TagSelector{Key: "env", Value: "prod"}, want: true},
		{name: "missing key", tags: map[string]string{"team": "growth"}, selector: TagSelector{Key: "env", Value: "prod"}, want: false},
		{name: "wrong value", tags: map[string]string{"env": "staging"}, selector: TagSelector{Key: "env", Value: "prod"}, want: false},
		{name: "key case mismatch", tags: map[string]string{"ENV": "prod"}, selector: TagSelector{Key: "env", Value: "prod"}, want: false},
		{name: "value case mismatch", tags: map[string]string{"env": "PROD"}, selector: TagSelector{Key: "env", Value: "prod"}, want: false},
		{name: "nil tag map", tags: nil, selector: TagSelector{Key: "env", Value: "prod"}, want: false},
		{name: "empty value matches empty selector value", tags: map[string]string{"env": ""}, selector: TagSelector{Key: "env", Value: ""}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.selector.Matches(tc.tags); got != tc.want {
				t.Errorf("Matches(%v) for %v = %v, want %v", tc.tags, tc.selector, got, tc.want)
			}
		})
	}
}

// TestMatchesAll_AllOfSemantics pins the AND-conjunction — every
// selector must match for the resource to pass. Empty selector slice
// is the no-filter fast path and matches anything (including a nil
// tag map). One mismatched selector in a multi-selector set rejects
// the resource even when every other selector matches.
func TestMatchesAll_AllOfSemantics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		tags      map[string]string
		selectors []TagSelector
		want      bool
	}{
		{name: "empty selectors match anything", tags: nil, selectors: nil, want: true},
		{name: "empty selectors match populated tags", tags: map[string]string{"env": "prod"}, selectors: nil, want: true},
		{name: "single selector hit", tags: map[string]string{"env": "prod"}, selectors: []TagSelector{{Key: "env", Value: "prod"}}, want: true},
		{name: "single selector miss", tags: map[string]string{"env": "staging"}, selectors: []TagSelector{{Key: "env", Value: "prod"}}, want: false},
		{name: "multi-selector all hit", tags: map[string]string{"env": "prod", "team": "growth"}, selectors: []TagSelector{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}}, want: true},
		{name: "multi-selector last miss rejects", tags: map[string]string{"env": "prod", "team": "infra"}, selectors: []TagSelector{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}}, want: false},
		// Symmetric to "last miss" — kills the "first-selector-only"
		// mutant (a buggy implementation that returns true after the
		// first hit without checking the rest, OR returns false only
		// when the first selector misses).
		{name: "multi-selector first miss rejects", tags: map[string]string{"env": "staging", "team": "growth"}, selectors: []TagSelector{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}}, want: false},
		// Programmatic callers can hand MatchesAll a duplicate-key
		// selector slice (the CLI parser rejects it, but Go callers
		// have no such gate). The AND-conjunction makes the conflict
		// unsatisfiable; the function must report false regardless of
		// which value the tag map carries.
		{name: "duplicate key conflicting values", tags: map[string]string{"env": "prod"}, selectors: []TagSelector{{Key: "env", Value: "prod"}, {Key: "env", Value: "staging"}}, want: false},
		{name: "nil tags with non-empty selectors fails", tags: nil, selectors: []TagSelector{{Key: "env", Value: "prod"}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchesAll(tc.tags, tc.selectors); got != tc.want {
				t.Errorf("MatchesAll(%v, %v) = %v, want %v", tc.tags, tc.selectors, got, tc.want)
			}
		})
	}
}
