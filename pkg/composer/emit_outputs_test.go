package composer

import (
	"regexp"
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
)

// outputBlock represents a parsed output block for structural assertions.
type outputBlock struct {
	name        string
	valueExpr   string // raw expression text (e.g. "module.ec2.instance_id")
	description string
	sensitive   bool
}

// parseOutputBlocks extracts output blocks from generated HCL for structural assertions.
// This avoids the weak "Contains" pattern that can't detect swapped values.
func parseOutputBlocks(t *testing.T, src []byte) []outputBlock {
	t.Helper()
	f, diags := hclsyntax.ParseConfig(src, "test.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "parseOutputBlocks: %s", diags.Error())

	body := f.Body.(*hclsyntax.Body)
	var out []outputBlock
	for _, blk := range body.Blocks {
		if blk.Type != "output" || len(blk.Labels) != 1 {
			continue
		}
		ob := outputBlock{name: blk.Labels[0]}
		attrs := blk.Body.Attributes

		if a, ok := attrs["value"]; ok {
			// Extract the raw source text of the value expression
			ob.valueExpr = string(src[a.Expr.Range().Start.Byte:a.Expr.Range().End.Byte])
		}
		if a, ok := attrs["description"]; ok {
			if lit, ok := a.Expr.(*hclsyntax.TemplateExpr); ok && len(lit.Parts) == 1 {
				if s, ok := lit.Parts[0].(*hclsyntax.LiteralValueExpr); ok {
					ob.description = s.Val.AsString()
				}
			}
		}
		if a, ok := attrs["sensitive"]; ok {
			if lit, ok := a.Expr.(*hclsyntax.LiteralValueExpr); ok {
				ob.sensitive = lit.Val.True()
			}
		}
		out = append(out, ob)
	}
	return out
}

// findOutput returns the output block with the given name, or fails the test.
func findOutput(t *testing.T, blocks []outputBlock, name string) outputBlock {
	t.Helper()
	for _, b := range blocks {
		if b.name == name {
			return b
		}
	}
	names := make([]string, len(blocks))
	for i, b := range blocks {
		names[i] = b.name
	}
	t.Fatalf("output %q not found in blocks: %v", name, names)
	return outputBlock{}
}

func TestEmitRootOutputsTF_SingleModule(t *testing.T) {
	t.Parallel()

	modules := []ModuleOutputs{
		{
			Module: "ec2",
			Outputs: []OutputMeta{
				{Name: "instance_id", Description: "The EC2 instance ID"},
				{Name: "public_ip", Description: "The public IP address"},
			},
		},
	}

	result := EmitRootOutputsTF(modules)
	require.NoError(t, parseHCL("outputs.tf", result))

	blocks := parseOutputBlocks(t, result)
	require.Len(t, blocks, 2)

	// Verify each output maps to the correct module.output (not just present somewhere)
	b := findOutput(t, blocks, "ec2_instance_id")
	require.Equal(t, "module.ec2.instance_id", b.valueExpr)
	require.Equal(t, "The EC2 instance ID", b.description)

	b = findOutput(t, blocks, "ec2_public_ip")
	require.Equal(t, "module.ec2.public_ip", b.valueExpr)
	require.Equal(t, "The public IP address", b.description)
}

func TestEmitRootOutputsTF_MultipleModules(t *testing.T) {
	t.Parallel()

	modules := []ModuleOutputs{
		{
			Module: "vpc",
			Outputs: []OutputMeta{
				{Name: "vpc_id", Description: "The VPC ID"},
			},
		},
		{
			Module: "rds",
			Outputs: []OutputMeta{
				{Name: "instance_id", Description: "RDS instance ID"},
				{Name: "db_password", Description: "Database password", Sensitive: true},
			},
		},
	}

	result := EmitRootOutputsTF(modules)
	require.NoError(t, parseHCL("outputs.tf", result))

	blocks := parseOutputBlocks(t, result)
	require.Len(t, blocks, 3)

	b := findOutput(t, blocks, "vpc_vpc_id")
	require.Equal(t, "module.vpc.vpc_id", b.valueExpr)

	b = findOutput(t, blocks, "rds_instance_id")
	require.Equal(t, "module.rds.instance_id", b.valueExpr)

	b = findOutput(t, blocks, "rds_db_password")
	require.Equal(t, "module.rds.db_password", b.valueExpr)
	require.True(t, b.sensitive, "rds_db_password should be sensitive")
}

func TestEmitRootOutputsTF_PreservesSensitive(t *testing.T) {
	t.Parallel()

	modules := []ModuleOutputs{
		{
			Module: "rds",
			Outputs: []OutputMeta{
				{Name: "password", Description: "DB password", Sensitive: true},
				{Name: "endpoint", Description: "DB endpoint", Sensitive: false},
			},
		},
	}

	result := EmitRootOutputsTF(modules)
	require.NoError(t, parseHCL("outputs.tf", result))

	blocks := parseOutputBlocks(t, result)
	require.Len(t, blocks, 2)

	// Verify sensitive is on the correct block, not just present somewhere
	pw := findOutput(t, blocks, "rds_password")
	require.True(t, pw.sensitive, "rds_password should be sensitive")

	ep := findOutput(t, blocks, "rds_endpoint")
	require.False(t, ep.sensitive, "rds_endpoint should NOT be sensitive")
}

func TestEmitRootOutputsTF_NoDescription(t *testing.T) {
	t.Parallel()

	modules := []ModuleOutputs{
		{
			Module: "s3",
			Outputs: []OutputMeta{
				{Name: "bucket_arn"},
			},
		},
	}

	result := EmitRootOutputsTF(modules)
	require.NoError(t, parseHCL("outputs.tf", result))

	blocks := parseOutputBlocks(t, result)
	require.Len(t, blocks, 1)

	b := findOutput(t, blocks, "s3_bucket_arn")
	require.Equal(t, "module.s3.bucket_arn", b.valueExpr)
	require.Empty(t, b.description)
	require.NotRegexp(t, regexp.MustCompile(`(?m)^\s*description\s*=`), string(result),
		"no description attribute should be emitted")
}

func TestEmitRootOutputsTF_Empty(t *testing.T) {
	t.Parallel()

	result := EmitRootOutputsTF(nil)
	require.Empty(t, strings.TrimSpace(string(result)))
}

func TestEmitRootOutputsTF_ModuleWithEmptyOutputs(t *testing.T) {
	t.Parallel()

	modules := []ModuleOutputs{
		{Module: "vpc", Outputs: nil},
		{Module: "ec2", Outputs: []OutputMeta{
			{Name: "id", Description: "Instance ID"},
		}},
	}

	result := EmitRootOutputsTF(modules)
	require.NoError(t, parseHCL("outputs.tf", result))

	blocks := parseOutputBlocks(t, result)
	require.Len(t, blocks, 1, "module with nil outputs should produce no output blocks")

	b := findOutput(t, blocks, "ec2_id")
	require.Equal(t, "module.ec2.id", b.valueExpr)
}
