package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExactPin_Accepted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"= 5.70.0", "5.70.0"},
		{"=5.70.0", "5.70.0"},
		{"5.70.0", "5.70.0"},
		{"  = 5.70.0  ", "5.70.0"},
		{"= 6.10.0-beta1", "6.10.0-beta1"},
		{"= 1.2.3+meta.build", "1.2.3+meta.build"},
		{"= 1.2.3-rc1+build.4", "1.2.3-rc1+build.4"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := parseExactPin(tc.in)
			require.NoErrorf(t, err, "parseExactPin(%q)", tc.in)
			assert.Equalf(t, tc.want, got, "parseExactPin(%q)", tc.in)
		})
	}
}

func TestParseExactPin_Rejected(t *testing.T) {
	t.Parallel()
	// Every rejected case must surface errNotExactPin so callers can
	// branch on the class of failure. The substring assertions on
	// other error classes (missing provider, source mismatch) live in
	// TestExtractExactPin below.
	cases := []string{
		">= 5.70.0",
		"> 5.70.0",
		"<= 5.70.0",
		"< 5.70.0",
		"!= 5.70.0",
		"~> 5.70.0",
		"~> 5.70",
		"== 5.70.0",
		"=== 5.70.0",
		">= 5.0, < 6.0",
		"= ",
		"=   ",
		"v5.70.0",
		"-5.70.0",
		"5.70.0a",
		"5.70",     // missing patch
		"5",        // missing minor + patch
		"5.70.0.1", // extra segment
		"5.70.0\n5.71.0",
		"version",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got, err := parseExactPin(in)
			require.Errorf(t, err, "parseExactPin(%q) should error, got %q", in, got)
			assert.ErrorIs(t, err, errNotExactPin,
				"parseExactPin(%q) must wrap errNotExactPin, got %v", in, err)
		})
	}
}

// TestParseExactPin_Empty pins the early-exit branch: blank input
// errors with a distinct (non-errNotExactPin) message because there's
// nothing to classify yet.
func TestParseExactPin_Empty(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "   "} {
		_, err := parseExactPin(in)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	}
}

func TestLoadProviderPins(t *testing.T) {
	t.Parallel()

	type want struct {
		pins      ProviderPins
		errMsg    string // substring; empty means expect success
		errIsKind error  // optional sentinel to assert via errors.Is
	}
	cases := []struct {
		name string
		body string
		want want
	}{
		{
			name: "happy path",
			body: `
terraform {
  required_providers {
    aws         = { source = "hashicorp/aws", version = "= 5.70.0" }
    google      = { source = "hashicorp/google", version = "= 6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "= 6.10.0" }
  }
}
`,
			want: want{pins: ProviderPins{AWS: "5.70.0", Google: "6.10.0", GoogleBeta: "6.10.0"}},
		},
		{
			name: "bare versions",
			body: `
terraform {
  required_providers {
    aws         = { source = "hashicorp/aws", version = "5.70.0" }
    google      = { source = "hashicorp/google", version = "6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "6.10.0" }
  }
}
`,
			want: want{pins: ProviderPins{AWS: "5.70.0", Google: "6.10.0", GoogleBeta: "6.10.0"}},
		},
		{
			name: "aws missing",
			body: `
terraform {
  required_providers {
    google      = { source = "hashicorp/google", version = "= 6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "= 6.10.0" }
  }
}
`,
			want: want{errMsg: `provider "aws": not declared`},
		},
		{
			name: "aws wrong source",
			body: `
terraform {
  required_providers {
    aws         = { source = "foo/aws", version = "= 5.70.0" }
    google      = { source = "hashicorp/google", version = "= 6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "= 6.10.0" }
  }
}
`,
			want: want{errMsg: `source mismatch`},
		},
		{
			name: "range constraint",
			body: `
terraform {
  required_providers {
    aws         = { source = "hashicorp/aws", version = ">= 5.70.0" }
    google      = { source = "hashicorp/google", version = "= 6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "= 6.10.0" }
  }
}
`,
			want: want{errIsKind: errNotExactPin},
		},
		{
			name: "pessimistic constraint",
			body: `
terraform {
  required_providers {
    aws         = { source = "hashicorp/aws", version = "~> 5.70" }
    google      = { source = "hashicorp/google", version = "= 6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "= 6.10.0" }
  }
}
`,
			want: want{errIsKind: errNotExactPin},
		},
		{
			name: "comma-joined range",
			body: `
terraform {
  required_providers {
    aws         = { source = "hashicorp/aws", version = ">= 5.0, < 6.0" }
    google      = { source = "hashicorp/google", version = "= 6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "= 6.10.0" }
  }
}
`,
			want: want{errIsKind: errNotExactPin},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "providers.tf")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o644))
			got, err := LoadProviderPins(path)
			if tc.want.errIsKind != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.want.errIsKind)
				return
			}
			if tc.want.errMsg != "" {
				require.Error(t, err, "expected error containing %q", tc.want.errMsg)
				assert.Contains(t, err.Error(), tc.want.errMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want.pins, got)
		})
	}
}

// TestLoadProviderPins_FileOnly pins the load-bearing "only the named
// file is parsed" property: a sibling .tf in the same directory that
// declares a *different* constraint must NOT aggregate into the
// result. This was the silent-multi-file-aggregation footgun called
// out in code review — without this guarantee, an unrelated .tf in
// the schemas/ directory could shadow providers.tf's pins.
func TestLoadProviderPins_FileOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(`
terraform {
  required_providers {
    aws         = { source = "hashicorp/aws", version = "= 5.70.0" }
    google      = { source = "hashicorp/google", version = "= 6.10.0" }
    google-beta = { source = "hashicorp/google-beta", version = "= 6.10.0" }
  }
}
`), 0o644))
	// Sibling declares a conflicting aws version. If LoadProviderPins
	// switched back to module-level aggregation, the resulting
	// VersionConstraints list would have len() == 2 and we'd surface
	// the "expected exactly one" error.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "noise.tf"), []byte(`
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "= 5.99.0" }
  }
}
`), 0o644))
	got, err := LoadProviderPins(filepath.Join(dir, "providers.tf"))
	require.NoError(t, err)
	assert.Equal(t, ProviderPins{AWS: "5.70.0", Google: "6.10.0", GoogleBeta: "6.10.0"}, got)
}

// TestExtractExactPin_MultiConstraint exercises the
// "more than one version constraint" branch directly with a
// hand-built tfconfig map, decoupling the test from tfconfig's
// aggregation semantics.
func TestExtractExactPin_MultiConstraint(t *testing.T) {
	t.Parallel()
	reqs := map[string]*tfconfig.ProviderRequirement{
		"aws": {
			Source:             "hashicorp/aws",
			VersionConstraints: []string{"= 5.70.0", "= 5.71.0"},
		},
	}
	_, err := extractExactPin(reqs, "aws")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `provider "aws"`)
	assert.Contains(t, err.Error(), "expected exactly one")
}

// TestExtractExactPin_UnknownTarget pins the fail-fast guard against
// callers passing a provider name outside providerSources.
func TestExtractExactPin_UnknownTarget(t *testing.T) {
	t.Parallel()
	_, err := extractExactPin(map[string]*tfconfig.ProviderRequirement{}, "azurerm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a recognized imported-codegen target")
}

// TestLoadProviderPins_FileNotFound pins the file-existence path:
// we must surface an HCL parse failure (not a misleading
// "provider not declared" cascade) when the named file is absent.
func TestLoadProviderPins_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := LoadProviderPins(filepath.Join(t.TempDir(), "does-not-exist.tf"))
	require.Error(t, err)
	// HCL's diagnostic format includes the filename — assert it
	// surfaces rather than the downstream "provider \"aws\": not
	// declared" error.
	assert.Contains(t, err.Error(), "parse")
}

