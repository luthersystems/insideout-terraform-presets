package composer

import (
	"encoding/json"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plannableIAMPolicyIR builds an aws_iam_policy ImportedResource whose Attrs
// carry the Required `policy` argument. It deliberately leaves ir.Tier
// unset — the issue #656 contract is that plannability is assessable
// directly off raw discovery output, with no Tier stamped.
func plannableIAMPolicyIR(t *testing.T) imported.ImportedResource {
	t.Helper()
	raw := json.RawMessage(`{"name":{"literal":"io-policy"},"policy":{"literal":"{\"Version\":\"2012-10-17\"}"}}`)
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "aws",
			Type:    "aws_iam_policy",
			Address: "aws_iam_policy.app",
		},
		Attrs: raw,
	}
}

// unplannableIAMPolicyIR builds the live repro from issue #656: an
// aws_iam_policy whose Attrs omit the Required `policy` argument. Tier is
// left unset on purpose.
func unplannableIAMPolicyIR(t *testing.T) imported.ImportedResource {
	t.Helper()
	raw := json.RawMessage(`{"name":{"literal":"io-policy"}}`)
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "aws",
			Type:    "aws_iam_policy",
			Address: "aws_iam_policy.app",
		},
		Attrs: raw,
	}
}

func TestMissingRequiredAttrs_Plannable(t *testing.T) {
	t.Parallel()
	ir := plannableIAMPolicyIR(t)
	require.Empty(t, ir.Tier, "contract: plannability is assessed without a stamped Tier")

	assert.Empty(t, MissingRequiredAttrs(ir))
	assert.True(t, Plannable(ir))
	assert.Equal(t, "", UnplannableReason(ir))
}

func TestMissingRequiredAttrs_Unplannable(t *testing.T) {
	t.Parallel()
	ir := unplannableIAMPolicyIR(t)
	require.Empty(t, ir.Tier, "contract: plannability is assessed without a stamped Tier")

	missing := MissingRequiredAttrs(ir)
	assert.Equal(t, []string{"policy"}, missing)
	assert.False(t, Plannable(ir))

	reason := UnplannableReason(ir)
	assert.Contains(t, reason, "policy")
	assert.Contains(t, reason, "not plannable")
	assert.Contains(t, reason, "aws_iam_policy.app")
}

// TestMissingRequiredAttrs_UnregisteredTypeIsPlannable asserts that a
// resource whose Terraform type is not a registered imported type is
// treated as an absent signal — plannable, with no missing arguments and no
// reason — so downstream callers do not disable its row.
func TestMissingRequiredAttrs_UnregisteredTypeIsPlannable(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "aws",
			Type:    "aws_not_a_real_imported_type",
			Address: "aws_not_a_real_imported_type.x",
		},
		Attrs: json.RawMessage(`{}`),
	}
	require.Empty(t, ir.Tier)

	assert.Nil(t, MissingRequiredAttrs(ir))
	assert.True(t, Plannable(ir))
	assert.Equal(t, "", UnplannableReason(ir))
}
