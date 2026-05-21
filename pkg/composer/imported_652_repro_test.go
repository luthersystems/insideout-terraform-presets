package composer

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestImported652Repro pins the composer's handling of the exact
// discovery manifest that produced the malformed imported.tf in #652
// (Reliable session sess_v2_CnqUJ6NRJnLC). testdata/imported_652_repro.json
// is a faithful reduction of stack_versions.imported for that session,
// carrying the three defect-bearing resources:
//
//   - aws_cloudwatch_log_group whose typed Attrs include the computed
//     `id` attribute — EmitImportedTF must NOT render it into the
//     resource body, or terraform plan fails "Invalid or unknown key".
//   - aws_iam_policy with no `policy` attribute (a required argument).
//   - aws_lambda_function with null Attrs — no `role`/`function_name`.
//
// The composer must (a) strip `id` from every emitted resource body and
// (b) surface the missing required arguments as
// imported_resource_missing_required_attr at compose time, rather than
// letting terraform plan fail later with an opaque error.
func TestImported652Repro(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/imported_652_repro.json")
	require.NoError(t, err)
	var irs []imported.ImportedResource
	require.NoError(t, json.Unmarshal(raw, &irs))
	require.Len(t, irs, 4)

	out, used := EmitImportedTF("aws", irs, EmitImportedOpts{
		ImportProjectID: "io-cnquj6nrjnlc",
		ImportSessionID: "sess_v2_CnqUJ6NRJnLC",
		ImportedAt:      time.Date(2026, 5, 20, 3, 52, 35, 0, time.UTC),
	})
	require.NotNil(t, out)
	assert.True(t, used["aws"])

	file, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.Falsef(t, diags.HasErrors(), "imported.tf must parse: %s\n%s", diags.Error(), out)

	// Bug 1: no `resource {}` body may carry the computed `id` attribute.
	// Terraform rejects `id = "..."` inside a resource block. import {}
	// blocks legitimately carry id — only resource blocks are checked.
	body := file.Body.(*hclsyntax.Body)
	resourceBlocks := 0
	for _, blk := range body.Blocks {
		if blk.Type != "resource" {
			continue
		}
		resourceBlocks++
		_, hasID := blk.Body.Attributes["id"]
		assert.Falsef(t, hasID,
			"resource %v emits the computed `id` attribute; #652 — EmitImportedTF must strip it", blk.Labels)
	}
	assert.Equal(t, 4, resourceBlocks, "expected one resource block per imported resource")

	// Bugs 2 & 3: the missing required arguments must be surfaced at
	// compose time. aws_iam_policy lacks `policy`; aws_lambda_function
	// has null Attrs (lacks `role` and `function_name`).
	issues := ValidateImportedEmitReadiness("aws", irs)
	flagged := map[string]bool{}
	for _, is := range issues {
		if is.Code == "imported_resource_missing_required_attr" {
			flagged[is.Field] = true
		}
	}
	// Expected field keys are derived from the loaded manifest rather
	// than hard-coded, so a change to address derivation can't silently
	// turn these assertions vacuous.
	wantFlagged := map[string]string{} // tfType -> imported.<address>
	for _, ir := range irs {
		switch ir.Identity.Type {
		case "aws_iam_policy", "aws_lambda_function":
			wantFlagged[ir.Identity.Type] = "imported." + ir.Identity.Address
		}
	}
	require.Len(t, wantFlagged, 2, "fixture must contain the iam_policy and lambda records")
	assert.Truef(t, flagged[wantFlagged["aws_iam_policy"]],
		"aws_iam_policy missing required `policy` not flagged at compose time; issues=%+v", issues)
	assert.Truef(t, flagged[wantFlagged["aws_lambda_function"]],
		"aws_lambda_function missing required args not flagged at compose time; issues=%+v", issues)
}

// TestImported652Repro_ComposeRefusesUncomposable is the end-to-end
// composer-side reproduction of #652: it runs the exact #652 discovery
// manifest through the public ComposeStackWithIssues path and asserts
// the composer refuses the un-composable resources. Against main —
// before the dropUncomposable hardening — the aws_iam_policy (no
// `policy`) and aws_lambda_function (null Attrs) are emitted as partial
// resource blocks and `terraform plan` aborts the entire stack with
// "Missing required argument". The composable aws_cloudwatch_log_group
// and aws_s3_bucket must survive so the rest of the stack still plans.
func TestImported652Repro_ComposeRefusesUncomposable(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/imported_652_repro.json")
	require.NoError(t, err)
	var irs []imported.ImportedResource
	require.NoError(t, json.Unmarshal(raw, &irs))

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "io-cnquj6nrjnlc",
		Region:       "us-east-1",
		Imported:     irs,
	})
	require.NoError(t, err)
	tf := string(res.Files["/imported.tf"])

	// Composable resources survive into the emitted stack.
	assert.Contains(t, tf, `resource "aws_cloudwatch_log_group"`,
		"the composable log group must be emitted")
	assert.Contains(t, tf, `resource "aws_s3_bucket"`,
		"the composable bucket must be emitted")

	// Un-composable resources are refused — emitting their partial
	// blocks would abort `terraform plan` for the whole stack (#652).
	assert.NotContains(t, tf, `resource "aws_iam_policy"`,
		"the iam_policy with no `policy` argument must be refused")
	assert.NotContains(t, tf, `resource "aws_lambda_function"`,
		"the lambda with null Attrs must be refused")

	// The refusal is explained, not silent.
	missingReqd := 0
	for _, is := range res.Issues {
		if is.Code == "imported_resource_missing_required_attr" {
			missingReqd++
		}
	}
	assert.GreaterOrEqualf(t, missingReqd, 2,
		"compose must report imported_resource_missing_required_attr for each refused resource; issues=%+v", res.Issues)
}
