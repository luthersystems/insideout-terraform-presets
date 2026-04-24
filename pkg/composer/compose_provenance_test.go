package composer

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComposedRoot_DeclaresProvenanceVariables pins the contract between the
// three systems that cooperate on the composed Terraform archive:
//
//   - template_ref   — written by ui-core into common.auto.tfvars.json, read
//                      by sandbox-infrastructure-template's shell scripts via
//                      getTfVar (see ui-core#289, sandbox-infra-template#94).
//                      Default "" keeps the variable optional for standalone
//                      applies (getTfVar falls back to "unknown").
//
//   - presets_ref    — self-reported version of insideout-terraform-presets
//                      at compose time via debug.ReadBuildInfo. Defaults to
//                      the actual version string so the deployed archive
//                      carries its own provenance without needing ui-core to
//                      write it.
//
// Missing either declaration triggers a "Value for undeclared variable"
// warning on every Oracle deploy (for template_ref) or loses drift-debug
// provenance (for presets_ref). See issues #107, #109.
func TestComposedRoot_DeclaresProvenanceVariables(t *testing.T) {
	t.Run("ComposeSingle", func(t *testing.T) {
		c := newTestClient(WithTerraformVersion("1.7.5"))
		out, err := c.ComposeSingle(ComposeSingleOpts{
			Cloud:   "aws",
			Key:     KeyAWSEKSNodeGroup,
			Comps:   &Components{},
			Cfg:     &Config{},
			Project: "demo",
			Region:  "us-east-1",
		})
		require.NoError(t, err)
		assertVariablesTFDeclaresProvenance(t, out)
	})

	t.Run("ComposeStack", func(t *testing.T) {
		c := newTestClient(WithTerraformVersion("1.7.5"))
		out, err := c.ComposeStack(ComposeStackOpts{
			Cloud:   "aws",
			Comps:   &Components{},
			Cfg:     &Config{},
			Project: "demo",
			Region:  "us-east-1",
		})
		require.NoError(t, err)
		assertVariablesTFDeclaresProvenance(t, out)
	})
}

func assertVariablesTFDeclaresProvenance(t *testing.T, out Files) {
	t.Helper()
	vars, ok := out["/variables.tf"]
	require.True(t, ok, "expected /variables.tf in composed bundle")
	require.NoError(t, parseHCL("variables.tf", vars), "variables.tf should parse")
	body := string(vars)

	// --- template_ref ---
	assert.Contains(t, body, `variable "template_ref"`,
		`variables.tf must declare "template_ref" so ui-core's common.auto.tfvars.json doesn't trigger a "Value for undeclared variable" warning`)
	// Empty default keeps the var optional — ui-core always writes a value at
	// deploy time, but standalone applies must not require it.
	assert.Regexp(t,
		regexp.MustCompile(`(?s)variable\s+"template_ref"\s*\{[^}]*default\s*=\s*""`),
		body,
		`template_ref must declare default = "" so the variable stays optional`)
	assert.Regexp(t,
		regexp.MustCompile(`(?s)variable\s+"template_ref"\s*\{[^}]*type\s*=\s*string`),
		body,
		`template_ref must be typed as string`)

	// --- presets_ref ---
	assert.Contains(t, body, `variable "presets_ref"`,
		`variables.tf must declare "presets_ref" to stamp the composer's own version into the deployed archive`)
	assert.Regexp(t,
		regexp.MustCompile(`(?s)variable\s+"presets_ref"\s*\{[^}]*type\s*=\s*string`),
		body,
		`presets_ref must be typed as string`)
	// Under `go test` the module is always in (devel) mode, so PresetsVersion
	// returns "" and the emitted default is the empty string. Production
	// builds (where ui-core imports the module as a dep) populate the real
	// version via debug.ReadBuildInfo — see TestPresetsVersionFromBuildInfo
	// for unit coverage of the non-empty branches.
	assert.Regexp(t,
		regexp.MustCompile(`(?s)variable\s+"presets_ref"\s*\{[^}]*default\s*=\s*""`),
		body,
		`presets_ref default is empty under in-tree test builds; unit test coverage of populated builds lives in version_test.go`)
}
