package composer

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPresetsVersionFromBuildInfo covers the decision tree without relying on
// the ambient `go test` BuildInfo (which is always "(devel)" for in-tree
// builds and would only exercise one of the five cases below).
func TestPresetsVersionFromBuildInfo(t *testing.T) {
	cases := []struct {
		name     string
		info     *debug.BuildInfo
		ok       bool
		expected string
	}{
		{
			name:     "build info unavailable",
			ok:       false,
			expected: "",
		},
		{
			name: "imported as dep with release tag",
			ok:   true,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/luthersystems/ui-core", Version: "v2.5.0"},
				Deps: []*debug.Module{
					{Path: "github.com/stretchr/testify", Version: "v1.9.0"},
					{Path: "github.com/luthersystems/insideout-terraform-presets", Version: "v1.4.2"},
				},
			},
			expected: "v1.4.2",
		},
		{
			name: "imported as dep with pseudo-version (untagged)",
			ok:   true,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/luthersystems/ui-core", Version: "v2.5.0"},
				Deps: []*debug.Module{
					{Path: "github.com/luthersystems/insideout-terraform-presets", Version: "v0.0.0-20260420000000-abc123def456"},
				},
			},
			expected: "v0.0.0-20260420000000-abc123def456",
		},
		{
			name: "self is main module with tagged release",
			ok:   true,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: selfModulePath, Version: "v1.4.2"},
			},
			expected: "v1.4.2",
		},
		{
			name: "self is main module in devel mode",
			ok:   true,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: selfModulePath, Version: "(devel)"},
			},
			expected: "",
		},
		{
			name: "self is main module with empty version",
			ok:   true,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: selfModulePath, Version: ""},
			},
			expected: "",
		},
		{
			name: "deps don't include self",
			ok:   true,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/example/something", Version: "v1.0.0"},
				Deps: []*debug.Module{
					{Path: "github.com/example/other", Version: "v0.1.0"},
				},
			},
			expected: "",
		},
		{
			name: "nil dep entries tolerated",
			ok:   true,
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/example/main", Version: "v1.0.0"},
				Deps: []*debug.Module{
					nil,
					{Path: "github.com/luthersystems/insideout-terraform-presets", Version: "v1.2.3"},
				},
			},
			expected: "v1.2.3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := presetsVersionFromBuildInfo(func() (*debug.BuildInfo, bool) {
				return tc.info, tc.ok
			})
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestPresetsVersion_RunsWithoutPanic exercises the public entry point to
// guard against a regression where debug.ReadBuildInfo() starts returning
// something the helper doesn't handle. The return value itself is ambient
// (always "" under `go test` in-tree) so we don't assert on it here.
func TestPresetsVersion_RunsWithoutPanic(t *testing.T) {
	_ = PresetsVersion()
}
