package composer

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComposedRoot_DeclaresTemplateRef pins the contract with ui-core: ui-core
// writes template_ref into common.auto.tfvars.json so downstream template
// scripts (sandbox-infrastructure-template's shell_utils.sh:getTfVar) can log
// the ref used to provision the project. The composer must declare the
// matching root variable, or every Oracle deploy emits a "Value for undeclared
// variable" warning. See issue #109.
func TestComposedRoot_DeclaresTemplateRef(t *testing.T) {
	t.Run("ComposeSingle", func(t *testing.T) {
		c := newTestClient(WithTerraformVersion("1.7.5"))
		out, err := c.ComposeSingle(ComposeSingleOpts{
			Cloud:   "aws",
			Key:     KeyEC2,
			Comps:   &Components{},
			Cfg:     &Config{},
			Project: "demo",
			Region:  "us-east-1",
		})
		require.NoError(t, err)
		assertVariablesTFDeclaresTemplateRef(t, out)
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
		assertVariablesTFDeclaresTemplateRef(t, out)
	})
}

func assertVariablesTFDeclaresTemplateRef(t *testing.T, out Files) {
	t.Helper()
	vars, ok := out["/variables.tf"]
	require.True(t, ok, "expected /variables.tf in composed bundle")

	require.NoError(t, parseHCL("variables.tf", vars), "variables.tf should parse")

	body := string(vars)
	assert.Contains(t, body, `variable "template_ref"`,
		`variables.tf must declare "template_ref" so ui-core's common.auto.tfvars.json doesn't trigger a "Value for undeclared variable" warning`)

	// Empty default keeps the var optional — ui-core always writes a value at
	// deploy time, but standalone applies must not require it.
	assert.Regexp(t,
		regexp.MustCompile(`(?s)variable\s+"template_ref"\s*\{[^}]*default\s*=\s*""`),
		body,
		`template_ref must declare default = "" so the variable stays optional`)

	// Type pin — getTfVar reads it as a string.
	assert.Regexp(t,
		regexp.MustCompile(`(?s)variable\s+"template_ref"\s*\{[^}]*type\s*=\s*string`),
		body,
		`template_ref must be typed as string`)
}
