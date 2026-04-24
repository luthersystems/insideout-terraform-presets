package composer

import (
	"regexp"
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// movedBlock is a parsed representation of a `moved {}` block.
type movedBlock struct {
	from, to string
}

// parseMovedBlocks extracts every `moved {}` block's from/to expressions
// from generated HCL. Uses the HCL parser rather than regex matching so a
// malformed block fails the test with a useful parse error.
func parseMovedBlocks(t *testing.T, src []byte) []movedBlock {
	t.Helper()
	f, diags := hclsyntax.ParseConfig(src, "test.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "parseMovedBlocks: %s", diags.Error())

	body := f.Body.(*hclsyntax.Body)
	var out []movedBlock
	for _, blk := range body.Blocks {
		if blk.Type != "moved" {
			continue
		}
		mb := movedBlock{}
		if a, ok := blk.Body.Attributes["from"]; ok {
			mb.from = strings.TrimSpace(string(src[a.Expr.Range().Start.Byte:a.Expr.Range().End.Byte]))
		}
		if a, ok := blk.Body.Attributes["to"]; ok {
			mb.to = strings.TrimSpace(string(src[a.Expr.Range().Start.Byte:a.Expr.Range().End.Byte]))
		}
		out = append(out, mb)
	}
	return out
}

func TestEmitRootMainTF_MovedBlocks_PrefixedModulesGetMoves(t *testing.T) {
	blocks := []ModuleBlock{
		{Name: "aws_vpc", Source: "./modules/vpc"},
		{Name: "aws_alb", Source: "./modules/alb"},
		{Name: "aws_rds", Source: "./modules/rds"},
	}
	out := EmitRootMainTF(blocks)
	moves := parseMovedBlocks(t, out)

	assert.Equal(t, []movedBlock{
		{from: "module.vpc", to: "module.aws_vpc"},
		{from: "module.alb", to: "module.aws_alb"},
		{from: "module.rds", to: "module.aws_rds"},
	}, moves, "each prefixed module should get a moved block from its legacy name")
}

func TestEmitRootMainTF_MovedBlocks_LegacyAndPolymorphicModulesSkipped(t *testing.T) {
	blocks := []ModuleBlock{
		{Name: "vpc", Source: "./modules/vpc"},           // legacy v1 module name: appendMovedBlocks matches only against v2 names, so no moved block
		{Name: "resource", Source: "./modules/eks"},      // polymorphic EKS control plane / Lambda runtime — not in legacyModuleRenames
		{Name: "ec2", Source: "./modules/eks_nodegroup"}, // polymorphic EKS node group — not in legacyModuleRenames
		{Name: "splunk", Source: "./modules/splunk"},     // third-party: no AWS-prefixed sibling
	}
	out := EmitRootMainTF(blocks)
	moves := parseMovedBlocks(t, out)
	assert.Empty(t, moves, "non-v2 module names should not produce moved blocks")
}

func TestEmitRootMainTF_MovedBlocks_MixedSelection(t *testing.T) {
	blocks := []ModuleBlock{
		{Name: "aws_vpc", Source: "./modules/vpc"},         // has moved
		{Name: "resource", Source: "./modules/eks"},        // skip
		{Name: "aws_bastion", Source: "./modules/bastion"}, // has moved
		{Name: "splunk", Source: "./modules/splunk"},       // skip
	}
	out := EmitRootMainTF(blocks)
	moves := parseMovedBlocks(t, out)

	require.Len(t, moves, 2)
	assert.Equal(t, movedBlock{from: "module.vpc", to: "module.aws_vpc"}, moves[0])
	assert.Equal(t, movedBlock{from: "module.bastion", to: "module.aws_bastion"}, moves[1])
}

func TestEmitRootMainTF_MovedBlocks_DeterministicOrder(t *testing.T) {
	// Iterating legacyModuleRenames directly would give non-deterministic
	// order (Go map iteration). appendMovedBlocks iterates the input slice,
	// so running twice must give byte-identical output.
	blocks := []ModuleBlock{
		{Name: "aws_s3", Source: "./modules/s3"},
		{Name: "aws_sqs", Source: "./modules/sqs"},
		{Name: "aws_dynamodb", Source: "./modules/dynamodb"},
		{Name: "aws_opensearch", Source: "./modules/opensearch"},
	}
	a := EmitRootMainTF(blocks)
	b := EmitRootMainTF(blocks)
	assert.Equal(t, a, b, "same inputs must produce identical output")

	// Moves must appear in input order, not map order.
	moves := parseMovedBlocks(t, a)
	assert.Equal(t, []movedBlock{
		{from: "module.s3", to: "module.aws_s3"},
		{from: "module.sqs", to: "module.aws_sqs"},
		{from: "module.dynamodb", to: "module.aws_dynamodb"},
		{from: "module.opensearch", to: "module.aws_opensearch"},
	}, moves)
}

func TestEmitRootMainTF_MovedBlocks_CountMatchesInputPrefixedBlocks(t *testing.T) {
	// Mutation-resistance: a regression that iterated legacyModuleRenames
	// directly (instead of iterating `blocks`) would emit moved blocks for
	// every v2 name regardless of whether it was in the rendered stack. That
	// produces `to = module.aws_foo` references pointing at modules that
	// don't exist, which `terraform validate` rejects. Pin the count
	// explicitly so the bug surfaces in unit tests, not just in integration.
	blocks := []ModuleBlock{{Name: "aws_vpc", Source: "./modules/vpc"}}
	moves := parseMovedBlocks(t, EmitRootMainTF(blocks))
	require.Len(t, moves, 1, "exactly one moved block when exactly one prefixed module is emitted")
	assert.Equal(t, movedBlock{from: "module.vpc", to: "module.aws_vpc"}, moves[0])
}

// expectedLegacyModuleRenames is the independently-authored shadow of the
// frozen legacyModuleRenames table. Keyed on the KeyAWS* ComponentKey
// constants so any drift between a row's hardcoded RHS in emit.go and the
// canonical ComponentKey.String() value is caught here, not in production
// Terraform state.
//
// Phase 4 freezes the moved-block bridge at 24 rows; any row added or
// removed must also update this map (and the cardinality assertion
// below).
var expectedLegacyModuleRenames = map[ComponentKey]string{
	KeyAWSVPC:                  "vpc",
	KeyAWSBastion:              "bastion",
	KeyAWSALB:                  "alb",
	KeyAWSCloudfront:           "cloudfront",
	KeyAWSWAF:                  "waf",
	KeyAWSRDS:                  "rds",
	KeyAWSElastiCache:          "elasticache",
	KeyAWSS3:                   "s3",
	KeyAWSDynamoDB:             "dynamodb",
	KeyAWSSQS:                  "sqs",
	KeyAWSMSK:                  "msk",
	KeyAWSCloudWatchLogs:       "cloudwatchlogs",
	KeyAWSCloudWatchMonitoring: "cloudwatchmonitoring",
	KeyAWSGrafana:              "grafana",
	KeyAWSCognito:              "cognito",
	KeyAWSBackups:              "backups",
	KeyAWSGitHubActions:        "githubactions",
	KeyAWSCodePipeline:         "codepipeline",
	KeyAWSLambda:               "lambda",
	KeyAWSAPIGateway:           "apigateway",
	KeyAWSKMS:                  "kms",
	KeyAWSSecretsManager:       "secretsmanager",
	KeyAWSOpenSearch:           "opensearch",
	KeyAWSBedrock:              "bedrock",
}

// TestLegacyModuleRenames_MatchesKeyAWSConstants pins the frozen map against
// an independent expected table keyed on the KeyAWS* ComponentKey constants.
// Catches three mutation classes the iterate-self test cannot:
//  1. row-delete (map cardinality drops)
//  2. value-flip (RHS stops matching ComponentKey.String())
//  3. drift of a ComponentKey.String() value on the constant side
//
// This is the authoritative source-of-truth check for the v0.4.0 migration
// window.
func TestLegacyModuleRenames_MatchesKeyAWSConstants(t *testing.T) {
	require.Len(t, legacyModuleRenames, len(expectedLegacyModuleRenames),
		"frozen legacyModuleRenames must have exactly %d rows; a row was added or deleted without updating expectedLegacyModuleRenames",
		len(expectedLegacyModuleRenames))

	for k, legacy := range expectedLegacyModuleRenames {
		t.Run(legacy+"_to_"+string(k), func(t *testing.T) {
			v2 := string(k)
			got, ok := legacyModuleRenames[legacy]
			require.True(t, ok,
				"legacyModuleRenames missing row for legacy=%q (expected → %s)", legacy, v2)
			require.Equal(t, v2, got,
				"legacyModuleRenames[%q] must equal %s.String()=%q; got %q",
				legacy, k, v2, got)
		})
	}
}

// TestEmitRootMainTF_MovedBlocks_EveryLegacyRenameEntry exercises the
// emitter path for every row of legacyModuleRenames — a rendered single-
// block stack with the v2 name must produce exactly one `moved { from =
// module.<legacy> to = module.<v2> }` block. Complements the map-vs-
// constants pin above: that test catches table drift; this one catches
// emitter-vs-table drift.
func TestEmitRootMainTF_MovedBlocks_EveryLegacyRenameEntry(t *testing.T) {
	for k, legacy := range expectedLegacyModuleRenames {
		v2 := string(k)
		t.Run(legacy+"_to_"+v2, func(t *testing.T) {
			blocks := []ModuleBlock{{Name: v2, Source: "./modules/" + legacy}}
			moves := parseMovedBlocks(t, EmitRootMainTF(blocks))
			require.Len(t, moves, 1,
				"exactly one moved block per rendered prefixed module (got %d for %s)", len(moves), v2)
			assert.Equal(t, movedBlock{
				from: "module." + legacy,
				to:   "module." + v2,
			}, moves[0])
		})
	}
}

func TestEmitRootMainTF_MovedBlocks_EmitsAfterModules(t *testing.T) {
	// The file layout convention is module blocks first, then moved blocks.
	// This makes the file readable and keeps moved blocks together.
	blocks := []ModuleBlock{
		{Name: "aws_vpc", Source: "./modules/vpc"},
		{Name: "aws_alb", Source: "./modules/alb"},
	}
	out := string(EmitRootMainTF(blocks))

	lastModule := strings.LastIndex(out, `module "aws_alb"`)
	firstMoved := regexp.MustCompile(`(?m)^moved\s*\{`).FindStringIndex(out)
	require.NotNil(t, firstMoved, "emitted file should contain at least one moved block")
	assert.Greater(t, firstMoved[0], lastModule, "moved blocks must appear after the last module block")
}
