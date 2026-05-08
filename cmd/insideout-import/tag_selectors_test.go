package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseTagSelectors_ValidShapes pins the happy paths — single
// selector, multiple selectors, whitespace tolerance, and the
// empty-input return (nil rather than empty slice; consistent with
// splitCSV's contract).
func TestParseTagSelectors_ValidShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want []tagSelectorPair
	}{
		{name: "empty input returns nil", raw: "", want: nil},
		{name: "whitespace input returns nil", raw: "   ", want: nil},
		{name: "single selector", raw: "env=prod", want: []tagSelectorPair{{Key: "env", Value: "prod"}}},
		{name: "two selectors", raw: "env=prod,team=growth", want: []tagSelectorPair{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}}},
		{name: "trims whitespace around pair", raw: " env = prod , team = growth ", want: []tagSelectorPair{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}}},
		{name: "value with embedded equals", raw: "url=https://example.com/?x=1", want: []tagSelectorPair{{Key: "url", Value: "https://example.com/?x=1"}}},
		{name: "empty value permitted", raw: "env=", want: []tagSelectorPair{{Key: "env", Value: ""}}},
		{name: "trailing comma is tolerated", raw: "env=prod,", want: []tagSelectorPair{{Key: "env", Value: "prod"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseTagSelectors(tc.raw)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseTagSelectors_RejectsMalformed pins each error path
// individually so a regression on one branch (e.g. silently dropping
// the missing-equals check) surfaces specifically rather than via a
// generic "no error returned" failure. Substring assertion on the
// error string keeps the messages owner-friendly without overfitting.
func TestParseTagSelectors_RejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		raw         string
		wantErrSubs string
	}{
		{name: "missing equals", raw: "env-prod", wantErrSubs: "missing '=' separator"},
		{name: "empty key", raw: "=prod", wantErrSubs: "empty key"},
		{name: "duplicate key", raw: "env=prod,env=staging", wantErrSubs: "duplicate key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseTagSelectors(tc.raw)
			if err == nil {
				t.Fatalf("expected error for %q", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Errorf("err=%q, want substring %q", err.Error(), tc.wantErrSubs)
			}
		})
	}
}
