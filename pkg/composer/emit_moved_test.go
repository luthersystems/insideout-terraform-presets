package composer

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmitRootMainTF_NoMovedBlocks verifies the baseline shape — when
// ModuleBlock.Moved is nil/empty, no `moved {}` block is emitted. This
// is the backwards-compat invariant: existing call sites that set zero-
// value Moved must produce identical output to before C3.
func TestEmitRootMainTF_NoMovedBlocks(t *testing.T) {
	got := EmitRootMainTF([]ModuleBlock{
		{Name: "aws_sqs", Source: "./aws/sqs"},
	})
	assert.NotContains(t, string(got), "moved",
		"EmitRootMainTF must not emit any moved {} block when ModuleBlock.Moved is empty")
}

// TestEmitRootMainTF_OneMovedBlock verifies a single moved block is
// emitted with the correct from / to expressions.
func TestEmitRootMainTF_OneMovedBlock(t *testing.T) {
	blocks := []ModuleBlock{
		{
			Name:   "aws_sqs",
			Source: "./aws/sqs",
			Moved: []MovedRef{
				{
					FromComponent: KeyAWSCloudWatchMonitoring,
					FromAddress:   `aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
					To:            `module.aws_sqs.aws_cloudwatch_metric_alarm.backlog["0"]`,
				},
			},
		},
	}
	got := string(EmitRootMainTF(blocks))

	assert.Contains(t, got, `moved {`,
		"emitted output should contain a moved {} block")
	assert.Contains(t, got, `from = module.aws_cloudwatch_monitoring.aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
		"emitted moved.from should match WireRef(FromComponent, FromAddress) — the prefix matches the declared module block label")
	assert.Contains(t, got, `to   = module.aws_sqs.aws_cloudwatch_metric_alarm.backlog["0"]`,
		"emitted moved.to should match the destination address verbatim")
}

// TestEmitRootMainTF_MultipleMovedBlocks verifies multiple moved blocks
// for one module each render their own block.
func TestEmitRootMainTF_MultipleMovedBlocks(t *testing.T) {
	blocks := []ModuleBlock{
		{
			Name:   "aws_rds",
			Source: "./aws/rds",
			Moved: []MovedRef{
				{
					FromComponent: KeyAWSCloudWatchMonitoring,
					FromAddress:   `aws_cloudwatch_metric_alarm.rds_cpu_high["0"]`,
					To:            `module.aws_rds.aws_cloudwatch_metric_alarm.cpu_high["0"]`,
				},
				{
					FromComponent: KeyAWSCloudWatchMonitoring,
					FromAddress:   `aws_cloudwatch_metric_alarm.rds_free_storage_low["0"]`,
					To:            `module.aws_rds.aws_cloudwatch_metric_alarm.free_storage_low["0"]`,
				},
			},
		},
	}
	got := string(EmitRootMainTF(blocks))
	count := strings.Count(got, "moved {")
	assert.Equal(t, 2, count,
		"two MovedRef entries should produce two moved {} blocks; got %d in:\n%s", count, got)
}

// TestEmitRootMainTF_MovedBlocksRoundTripParse verifies the emitted
// output is well-formed HCL that hclwrite can re-parse without errors.
// Catches accidental quoting / escaping bugs in setRawExpr.
func TestEmitRootMainTF_MovedBlocksRoundTripParse(t *testing.T) {
	blocks := []ModuleBlock{
		{
			Name:   "aws_sqs",
			Source: "./aws/sqs",
			Moved: []MovedRef{{
				FromComponent: KeyAWSCloudWatchMonitoring,
				FromAddress:   `aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
				To:            `module.aws_sqs.aws_cloudwatch_metric_alarm.backlog["0"]`,
			}},
		},
	}
	emitted := EmitRootMainTF(blocks)
	parsed, diags := hclwrite.ParseConfig(emitted, "test.tf", hcl.Pos{Line: 1, Column: 1})
	require.False(t, diags.HasErrors(),
		"emitted HCL should re-parse without errors; got diags: %s\nemitted:\n%s",
		diags.Error(), string(emitted))

	// Walk the parsed body and confirm a moved block exists.
	movedFound := false
	for _, b := range parsed.Body().Blocks() {
		if b.Type() == "moved" {
			movedFound = true
			break
		}
	}
	assert.True(t, movedFound, "parsed body must contain a moved block; emitted:\n%s", string(emitted))
}

// TestEmitRootMainTF_MovedFollowsModule verifies the moved block is
// emitted *after* the module block it relocates into, so reviewers
// reading the diff see the relocation context next to the destination.
func TestEmitRootMainTF_MovedFollowsModule(t *testing.T) {
	blocks := []ModuleBlock{
		{
			Name:   "aws_sqs",
			Source: "./aws/sqs",
			Moved: []MovedRef{{
				FromComponent: KeyAWSCloudWatchMonitoring,
				FromAddress:   `aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
				To:            `module.aws_sqs.aws_cloudwatch_metric_alarm.backlog["0"]`,
			}},
		},
	}
	got := string(EmitRootMainTF(blocks))
	moduleIdx := strings.Index(got, "module \"aws_sqs\"")
	movedIdx := strings.Index(got, "moved {")
	require.True(t, moduleIdx >= 0, "module block missing")
	require.True(t, movedIdx >= 0, "moved block missing")
	assert.Less(t, moduleIdx, movedIdx,
		"moved block must follow its associated module block; got moduleIdx=%d movedIdx=%d in:\n%s",
		moduleIdx, movedIdx, got)
}
