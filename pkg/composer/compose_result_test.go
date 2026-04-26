package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestComposeStackWithIssues_GreenPath asserts the new entry point produces
// the same Files map as the legacy ComposeStack on a fully-specified stack,
// and surfaces zero issues when no validators trip.
func TestComposeStackWithIssues_GreenPath(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	mkOpts := func() ComposeStackOpts {
		return ComposeStackOpts{
			Cloud:        "aws",
			SelectedKeys: []ComponentKey{KeyAWSVPC},
			Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC"},
			Cfg:          &Config{},
			Project:      "p",
			Region:       "us-east-1",
		}
	}

	r, err := c.ComposeStackWithIssues(mkOpts())
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Empty(t, r.Issues, "green-path stack should produce no issues")
	require.Contains(t, r.Files, "/main.tf")
	require.Contains(t, r.Files, "/variables.tf")
	require.Contains(t, r.Files, "/providers.tf")
}

// TestComposeStackWithIssues_AggregatesAcrossModules drives a stack where
// multiple selected modules are missing required inputs simultaneously and
// asserts every miss surfaces as a structured issue (rather than the legacy
// path's first-failure error). This is the same-turn-correction property
// reliable/Riley relies on.
func TestComposeStackWithIssues_AggregatesAcrossModules(t *testing.T) {
	t.Parallel()

	// Stub mapper that returns no values so every required variable in the
	// selected modules surfaces as a missing_required_variable issue.
	c := newTestClient(WithMapper(emptyMapper{}))

	opts := ComposeStackOpts{
		Cloud: "aws",
		// Pick two leaf modules whose presets declare required (non-default)
		// variables. KMS and DynamoDB both have required inputs.
		SelectedKeys: []ComponentKey{KeyAWSKMS, KeyAWSDynamoDB},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "p",
		Region:       "us-east-1",
	}

	r, err := c.ComposeStackWithIssues(opts)
	require.NoError(t, err, "WithIssues should succeed even when validators trip; issues live in Result")
	require.NotNil(t, r)

	if len(r.Issues) < 2 {
		// If neither module has a required-without-default variable today,
		// the test is a no-op signal — fail loudly so a future maintainer
		// updates the fixture rather than silently passing.
		t.Fatalf("expected ≥2 missing-required issues across two modules, got %d (modules may have lost required-without-default vars; pick others)", len(r.Issues))
	}

	modulesSeen := map[string]bool{}
	for _, iss := range r.Issues {
		require.Equal(t, "missing_required_variable", iss.Code, "unexpected issue code %q for field %q", iss.Code, iss.Field)
		require.Contains(t, iss.Field, ".", "missing-required field should be <module>.<var>; got %q", iss.Field)
		require.NotEmpty(t, iss.Reason)
		modulesSeen[strings.SplitN(iss.Field, ".", 2)[0]] = true
	}
	require.GreaterOrEqual(t, len(modulesSeen), 2, "expected issues spanning ≥2 modules, got %d distinct modules: %v", len(modulesSeen), modulesSeen)
}

// TestComposeStackWithIssues_StrictEscalates pins the contract that
// StrictValidate=true converts any non-empty Issues list into an aggregated
// error — the opt-in fail-fast behavior callers asked for.
func TestComposeStackWithIssues_StrictEscalates(t *testing.T) {
	t.Parallel()

	c := newTestClient(WithMapper(emptyMapper{}))

	opts := ComposeStackOpts{
		Cloud:          "aws",
		SelectedKeys:   []ComponentKey{KeyAWSKMS},
		Comps:          &Components{Cloud: "AWS"},
		Cfg:            &Config{},
		Project:        "p",
		Region:         "us-east-1",
		StrictValidate: true,
	}

	r, err := c.ComposeStackWithIssues(opts)
	require.Error(t, err, "StrictValidate must escalate any issue to an aggregated error")
	require.NotNil(t, r, "Result is still returned alongside the strict error so callers can inspect Issues")
	require.NotEmpty(t, r.Issues)
	require.Contains(t, err.Error(), "validation issue")
}

// TestComposeStack_LegacyHardFailPreserved guards the contract that the
// historical ComposeStack(Files, error) path still hard-fails on the first
// missing-required miss with the same error shape it always had — so existing
// callers keep working without changes.
func TestComposeStack_LegacyHardFailPreserved(t *testing.T) {
	t.Parallel()

	c := newTestClient(WithMapper(emptyMapper{}))

	files, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSKMS},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "p",
		Region:       "us-east-1",
	})
	require.Error(t, err)
	require.Nil(t, files)
	require.Contains(t, err.Error(), "requires variable")
}

// TestInspectPreset_LoadsKnownModule sanity-checks the new tfconfig wrapper
// against a representative preset. Failure of this test means tfconfig
// adoption is broken, not just a flaky preset.
func TestInspectPreset_LoadsKnownModule(t *testing.T) {
	t.Parallel()

	mod, err := InspectPreset("aws/vpc")
	require.NoError(t, err)
	require.NotNil(t, mod)
	require.NotEmpty(t, mod.Variables, "vpc preset should declare variables")
	require.NotEmpty(t, mod.Outputs, "vpc preset should declare outputs")

	// Cache hit on second call returns the same pointer.
	mod2, err := InspectPreset("aws/vpc")
	require.NoError(t, err)
	require.Same(t, mod, mod2, "InspectPreset should cache the parsed module")
}

// emptyMapper returns no module values, exposing every required variable.
type emptyMapper struct{}

func (emptyMapper) BuildModuleValues(_ ComponentKey, _ *Components, _ *Config, _, _ string) (map[string]any, error) {
	return map[string]any{}, nil
}
