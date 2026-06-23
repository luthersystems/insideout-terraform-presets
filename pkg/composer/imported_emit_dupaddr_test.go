package composer

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestEmitImportedTF_DuplicateAddressDeduped reproduces the prod
// reverse-import failure (sess_v2_Jok8JjJhzJER):
//
//	Error: Duplicate import configuration for
//	"aws_iam_role_policy_attachment.arn_aws_iam_aws_policy_administratoraccess_1e6a3817"
//
// When two ImportedResources carrying the SAME Terraform address reach
// EmitImportedTF, the emitter must NOT write two `import {}` (and two
// `resource {}`) blocks for that address — Terraform rejects a second
// `import { to = <addr> }` for an address already targeted by another
// import block. Two IRs sharing an Address are, by construction, the same
// logical resource: GenerateAddress's `_<8hex>` collision suffix is a
// slice of identityHash, which folds in Cloud/AccountID/Region/Type/
// ImportID — so an identical suffixed address implies an identical
// identity tuple. Collapsing to the first occurrence is therefore lossless.
func TestEmitImportedTF_DuplicateAddressDeduped(t *testing.T) {
	t.Parallel()

	const addr = "aws_iam_role_policy_attachment.arn_aws_iam_aws_policy_administratoraccess_1e6a3817"
	mk := func() imported.ImportedResource {
		return imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_iam_role_policy_attachment",
				Address:  addr,
				ImportID: "platform-test-admin/arn:aws:iam::aws:policy/AdministratorAccess",
				NativeIDs: map[string]string{
					"role":       "platform-test-admin",
					"policy_arn": "arn:aws:iam::aws:policy/AdministratorAccess",
				},
			},
			Tier: imported.TierImportedFlat,
			Attrs: []byte(`{"role":{"literal":"platform-test-admin"},` +
				`"policy_arn":{"literal":"arn:aws:iam::aws:policy/AdministratorAccess"}}`),
		}
	}

	out, used := EmitImportedTF("aws", []imported.ImportedResource{mk(), mk()}, EmitImportedOpts{})
	require.NotNil(t, out)
	require.True(t, used["aws"])
	s := string(out)

	// Exactly one import block and one resource block for the shared address.
	require.Equalf(t, 1, strings.Count(s, "to = "+addr),
		"expected a single import block for %s, got duplicates:\n%s", addr, s)
	require.Equalf(t, 1, strings.Count(s, `resource "aws_iam_role_policy_attachment" "arn_aws_iam_aws_policy_administratoraccess_1e6a3817"`),
		"expected a single resource block for %s, got duplicates:\n%s", addr, s)

	// And the emitted HCL must parse.
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "imported.tf must parse: %s", diags.Error())
}
