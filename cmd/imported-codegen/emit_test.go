package main

import (
	"strings"
	"testing"
)

// TestProviderSourceConstName_RoundTripsEveryKnownSource pins the
// invariant that every provider source declared in config.go has a
// matching switch case in providerSourceConstName. Adding a new
// Wanted* slice for a future provider (e.g. Azure) without
// extending this switch would otherwise miscategorize every
// generated .gen.go file's Register() call — the panic in the
// default case catches it at codegen run time, this test catches
// it at unit-test time.
func TestProviderSourceConstName_RoundTripsEveryKnownSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		src  string
		want string
	}{
		{src: AWSProviderSource, want: "AWSProviderSource"},
		{src: GoogleProviderSource, want: "GoogleProviderSource"},
		{src: GoogleBetaProviderSource, want: "GoogleBetaProviderSource"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			t.Parallel()
			got := providerSourceConstName(tc.src)
			if got != tc.want {
				t.Errorf("providerSourceConstName(%q)=%q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestProviderSourceConstName_UnknownPanics pins the fail-fast
// guard documented on the function: silent fallback was the prior
// shape and caused [P1] miscategorization risk (every type for a
// new provider routes through the wrong constant). The panic is
// the load-bearing contract — without it, the unit test above
// would still pass for any new source by silently picking the AWS
// constant.
func TestProviderSourceConstName_UnknownPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("providerSourceConstName must panic on unknown source")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value should be string, got %T: %v", r, r)
		}
		// Operator-actionable message: must name the bad source and
		// point at the fix (extending the switch).
		for _, want := range []string{"registry.terraform.io/hashicorp/azure", "providerSourceConstName"} {
			if !strings.Contains(msg, want) {
				t.Errorf("panic message %q must contain %q", msg, want)
			}
		}
	}()
	_ = providerSourceConstName("registry.terraform.io/hashicorp/azure")
}
