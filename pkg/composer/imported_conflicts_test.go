package composer

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// loadConflictFixture reads testdata/imported_conflicts.json — a faithful
// reduction of the real 86-resource whole-account manifest that produced the
// conflicting-argument imported.tf. It carries the two resources whose live
// state terraform import echoes back as mutually-exclusive provider
// attributes:
//
//   - aws_network_interface with BOTH private_ip_list AND private_ips (the
//     same IP): provider error "Conflicting configuration arguments".
//   - aws_lb with BOTH a subnets list AND subnet_mapping blocks: provider
//     error "Invalid combination of arguments".
//
// plus the dependency closure that makes the bundle a self-contained,
// resolvable graph so `terraform validate` doesn't mask the ENI conflict
// behind an unresolved reference: the two aws_subnet the ENIs sit in and the
// two aws_vpc those subnets belong to. An aws_s3_bucket rides along as
// realistic ballast (its inline server_side_encryption_configuration is only a
// deprecation warning, not a conflict).
func loadConflictFixture(t *testing.T) []imported.ImportedResource {
	t.Helper()
	raw, err := os.ReadFile("testdata/imported_conflicts.json")
	require.NoError(t, err)
	var irs []imported.ImportedResource
	require.NoError(t, json.Unmarshal(raw, &irs))
	require.NotEmpty(t, irs)
	return irs
}

// resourceBlocks parses imported.tf and returns every `resource "<wantType>"`
// block body keyed by its resource name label.
func resourceBlocks(t *testing.T, tf []byte, wantType string) map[string]*hclsyntax.Body {
	t.Helper()
	file, diags := hclsyntax.ParseConfig(tf, "imported.tf", hcl.InitialPos)
	require.Falsef(t, diags.HasErrors(), "imported.tf must parse: %s\n%s", diags.Error(), tf)
	out := map[string]*hclsyntax.Body{}
	for _, blk := range file.Body.(*hclsyntax.Body).Blocks {
		if blk.Type != "resource" || len(blk.Labels) != 2 || blk.Labels[0] != wantType {
			continue
		}
		out[blk.Labels[1]] = blk.Body
	}
	return out
}

func bodyHasAttr(b *hclsyntax.Body, name string) bool {
	_, ok := b.Attributes[name]
	return ok
}

func bodyHasBlock(b *hclsyntax.Body, typ string) bool {
	for _, blk := range b.Blocks {
		if blk.Type == typ {
			return true
		}
	}
	return false
}

// TestImportedConflicts_ComposePathNormalizes is the #708-composer-path
// regression test. It runs the exact reduced manifest through the PUBLIC
// composer entrypoint (ComposeStackWithIssues) and asserts the /imported.tf
// the composer writes has been normalized — the mutually-exclusive provider
// attributes are reconciled.
//
// The proof is before/after at the composer boundary: EmitImportedTF (the raw
// emit both compose paths share) still produces the conflicting pairs, but the
// composer's emitted /imported.tf — which now runs the shared
// normalize.NormalizeImportedHCL over that emit, the same pass the
// reverse-import pipeline runs — no longer does. Against main (before wiring
// the composer path) the composed /imported.tf is byte-identical to the raw
// emit and this test fails.
//
// Runs in normal CI: pure HCL parsing, no terraform binary.
func TestImportedConflicts_ComposePathNormalizes(t *testing.T) {
	t.Parallel()
	irs := loadConflictFixture(t)

	// Raw emit path (shared by both compose paths) — the un-normalized shape.
	raw, used := EmitImportedTF("aws", irs, EmitImportedOpts{})
	require.NotEmpty(t, raw)
	require.True(t, used["aws"])

	// Composer path — the artifact ui-core/reliable ship. This is what the
	// fix normalizes.
	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "io-f-v6e-hzw-zt",
		Region:       "us-east-1",
		Imported:     irs,
	})
	require.NoError(t, err)
	composed := res.Files["/imported.tf"]
	require.NotEmpty(t, composed, "composer must emit /imported.tf for the adopted resources")

	// --- aws_network_interface: private_ip_list conflicts with private_ips ---
	rawENIs := resourceBlocks(t, raw, "aws_network_interface")
	require.NotEmpty(t, rawENIs, "fixture must emit aws_network_interface blocks")
	sawRawConflict := false
	for name, b := range rawENIs {
		if bodyHasAttr(b, "private_ip_list") && bodyHasAttr(b, "private_ips") {
			sawRawConflict = true
			t.Logf("raw ENI %q carries both private_ip_list and private_ips (the bug)", name)
		}
	}
	require.True(t, sawRawConflict,
		"fixture no longer reproduces the ENI private_ip_list/private_ips conflict; "+
			"the after-assertion would be vacuous")

	composedENIs := resourceBlocks(t, composed, "aws_network_interface")
	require.NotEmpty(t, composedENIs, "composer must emit the aws_network_interface blocks")
	for name, b := range composedENIs {
		assert.Falsef(t, bodyHasAttr(b, "private_ip_list") && bodyHasAttr(b, "private_ips"),
			"composed aws_network_interface %q still emits BOTH private_ip_list and private_ips — "+
				"the composer path did not normalize the imported.tf (#708)", name)
		// The plural form carries the intent and must survive; the *_list form
		// is the redundant alternative that fixupNetworkInterfaceProviderQuirks drops.
		assert.Truef(t, bodyHasAttr(b, "private_ips"),
			"composed aws_network_interface %q dropped private_ips; the normalizer must keep the plural form", name)
		assert.Falsef(t, bodyHasAttr(b, "private_ip_list"),
			"composed aws_network_interface %q kept the redundant private_ip_list", name)
	}

	// --- aws_lb: subnets conflicts with subnet_mapping ---
	rawLBs := resourceBlocks(t, raw, "aws_lb")
	require.NotEmpty(t, rawLBs, "fixture must emit an aws_lb block")
	sawRawLBConflict := false
	for name, b := range rawLBs {
		if bodyHasAttr(b, "subnets") && bodyHasBlock(b, "subnet_mapping") {
			sawRawLBConflict = true
			t.Logf("raw LB %q carries both subnets and subnet_mapping (the bug)", name)
		}
	}
	require.True(t, sawRawLBConflict,
		"fixture no longer reproduces the aws_lb subnets/subnet_mapping conflict")

	composedLBs := resourceBlocks(t, composed, "aws_lb")
	require.NotEmpty(t, composedLBs, "composer must emit the aws_lb block")
	for name, b := range composedLBs {
		assert.Falsef(t, bodyHasAttr(b, "subnets") && bodyHasBlock(b, "subnet_mapping"),
			"composed aws_lb %q still emits BOTH subnets and subnet_mapping — "+
				"the composer path did not normalize the imported.tf (#708)", name)
		// No static-IP pin in the fixture, so the canonical ALB shape keeps
		// `subnets` and drops the subnet_mapping blocks.
		assert.Truef(t, bodyHasAttr(b, "subnets"),
			"composed aws_lb %q dropped subnets; with no static-IP pin the normalizer keeps subnets", name)
		assert.Falsef(t, bodyHasBlock(b, "subnet_mapping"),
			"composed aws_lb %q kept the redundant subnet_mapping blocks", name)
	}
}
