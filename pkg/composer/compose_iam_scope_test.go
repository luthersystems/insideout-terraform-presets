package composer

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
)

// The composed AWS root declares iam_scope (default null) and passes it to
// every module that declares it. The template writes its value for a
// project on the scoped deploy policy, so the presets put their IAM objects
// on the project path with the workload boundary
// (sandbox-infrastructure-template#147). With no value, the presets keep
// their old path and no boundary.
func TestComposeWiresIAMScope(t *testing.T) {
	t.Run("ComposeStack", func(t *testing.T) {
		c := newTestClient(WithTerraformVersion("1.7.5"))
		out, err := c.ComposeStack(ComposeStackOpts{
			Cloud:        "aws",
			SelectedKeys: []ComponentKey{KeyAWSLambda, KeyAWSS3},
			Comps:        &Components{AWSLambda: ptrBool(true), AWSS3: ptrBool(true)},
			Cfg:          &Config{},
			Project:      "demo",
			Region:       "us-east-1",
		})
		require.NoError(t, err)
		requireIAMScopeRootVar(t, out)
		inputs := moduleInputs(t, out["/main.tf"])
		require.Equal(t, "var.iam_scope", inputs["aws_lambda"]["iam_scope"], "lambda declares iam_scope")
		_, has := inputs["aws_s3"]["iam_scope"]
		require.False(t, has, "s3 declares no iam_scope, so it gets none")
	})

	t.Run("ComposeSingle", func(t *testing.T) {
		c := newTestClient(WithTerraformVersion("1.7.5"))
		out, err := c.ComposeSingle(ComposeSingleOpts{
			Cloud: "aws", Key: KeyAWSLambda, Comps: &Components{AWSLambda: ptrBool(true)},
			Cfg: &Config{}, Project: "demo", Region: "us-east-1",
		})
		require.NoError(t, err)
		requireIAMScopeRootVar(t, out)
		require.Equal(t, "var.iam_scope", moduleInputs(t, out["/main.tf"])["aws_lambda"]["iam_scope"])
	})

	t.Run("GCP has none", func(t *testing.T) {
		c := newTestClient(WithTerraformVersion("1.7.5"))
		out, err := c.ComposeStack(ComposeStackOpts{
			Cloud: "gcp", Comps: &Components{}, Cfg: &Config{}, Project: "demo", Region: "us-central1",
		})
		require.NoError(t, err)
		require.NotContains(t, string(out["/variables.tf"]), `variable "iam_scope"`)
	})
}

func requireIAMScopeRootVar(t *testing.T, out Files) {
	t.Helper()
	f, diags := hclsyntax.ParseConfig(out["/variables.tf"], "variables.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), diags.Error())
	for _, b := range f.Body.(*hclsyntax.Body).Blocks {
		if b.Type != "variable" || b.Labels[0] != "iam_scope" {
			continue
		}
		def, ok := b.Body.Attributes["default"]
		require.True(t, ok, "iam_scope needs a default, or every stack must set it")
		v, d := def.Expr.Value(nil)
		require.False(t, d.HasErrors())
		require.True(t, v.IsNull(), "iam_scope defaults to null")
		return
	}
	t.Fatal(`variables.tf has no variable "iam_scope"`)
}

// moduleInputs returns module name -> argument name -> expression source.
func moduleInputs(t *testing.T, src []byte) map[string]map[string]string {
	t.Helper()
	f, diags := hclsyntax.ParseConfig(src, "main.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), diags.Error())
	out := map[string]map[string]string{}
	for _, b := range f.Body.(*hclsyntax.Body).Blocks {
		if b.Type != "module" {
			continue
		}
		m := map[string]string{}
		for name, a := range b.Body.Attributes {
			r := a.Expr.Range()
			m[name] = strings.TrimSpace(string(src[r.Start.Byte:r.End.Byte]))
		}
		out[b.Labels[0]] = m
	}
	return out
}
