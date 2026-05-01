package composer

import (
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
)

// TestEKSNodeGroupHasAMIType asserts every aws_eks_node_group resource
// declared anywhere in the preset library sets an ami_type attribute.
//
// Without this guard, omitting ami_type causes the AWS provider to default
// to AL2023_x86_64_STANDARD. Picking a Graviton instance type (c7g.large,
// m7g.xlarge, etc.) then produces an architecture mismatch: workers never
// come up, aws-ebs-csi-driver and coredns sit DEGRADED until the 20m addon
// timeout fires. This was the root cause behind issue #207 (Dario's session
// sess_v2_hrbS5zpRBk51, job ccs-6ee757a6-mr95b — DEGRADED addons were the
// downstream symptom of the AMI/instance arch mismatch).
//
// Pure structural check — does NOT require AWS credentials, mocks, or real
// apply. Parses the embedded preset HCL offline.
func TestEKSNodeGroupHasAMIType(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	keys, err := c.ListPresetKeysForCloud("aws")
	require.NoError(t, err)

	checked := 0
	for _, presetPath := range keys {
		files, err := c.GetPresetFiles(presetPath)
		require.NoError(t, err, "GetPresetFiles(%s)", presetPath)

		for fileName, content := range files {
			if !strings.HasSuffix(fileName, ".tf") {
				continue
			}

			f, diags := hclsyntax.ParseConfig(content, fileName, hcl.InitialPos)
			require.False(t, diags.HasErrors(), "parse %s%s: %s", presetPath, fileName, diags.Error())

			body, ok := f.Body.(*hclsyntax.Body)
			if !ok {
				continue
			}

			for _, block := range body.Blocks {
				if block.Type != "resource" {
					continue
				}
				if len(block.Labels) < 2 || block.Labels[0] != "aws_eks_node_group" {
					continue
				}
				_, hasAMIType := block.Body.Attributes["ami_type"]
				require.True(t, hasAMIType,
					"%s%s: resource %q.%q must set ami_type — omitting it lets the provider default to AL2023_x86_64_STANDARD and breaks ARM/Graviton instance choices (issue #207)",
					presetPath, fileName, block.Labels[0], block.Labels[1])
				checked++
			}
		}
	}

	// Cardinality floor: the loop has to fire at least once for the guard
	// to be meaningful. If it falls to 0 (e.g. someone deletes the
	// eks_nodegroup preset entirely or the iteration shape silently
	// breaks), this test would otherwise pass vacuously and the
	// regression returns.
	require.GreaterOrEqual(t, checked, 1,
		"expected at least one aws_eks_node_group resource in the preset library; got 0 — test silently passing")
}
