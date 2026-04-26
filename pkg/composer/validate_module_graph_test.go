package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractWiringEdges_BasicAndDeterministic(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{
			Name: "aws_alb",
			Raw: map[string]string{
				"vpc_id":  "module.aws_vpc.vpc_id",
				"subnets": "module.aws_vpc.public_subnet_ids",
				// Mixed expression with multiple traversals; both should surface.
				"sg_ids": "concat(module.aws_vpc.default_sg, module.aws_kms.something)",
			},
		},
		{
			Name: "aws_rds",
			Raw: map[string]string{
				"vpc_id": "module.aws_vpc.vpc_id",
			},
		},
	}

	edges := extractWiringEdges(blocks)
	require.GreaterOrEqual(t, len(edges), 4)

	// Stable ordering (input keys sorted within each block).
	want := []wiringEdge{
		{Producer: "aws_vpc", Output: "default_sg", Consumer: "aws_alb", Input: "sg_ids"},
		{Producer: "aws_kms", Output: "something", Consumer: "aws_alb", Input: "sg_ids"},
		{Producer: "aws_vpc", Output: "public_subnet_ids", Consumer: "aws_alb", Input: "subnets"},
		{Producer: "aws_vpc", Output: "vpc_id", Consumer: "aws_alb", Input: "vpc_id"},
		{Producer: "aws_vpc", Output: "vpc_id", Consumer: "aws_rds", Input: "vpc_id"},
	}
	require.Equal(t, want, edges)
}

func TestValidateModuleWiring_FlagsMissingOutput(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{
			Name: "aws_alb",
			Raw: map[string]string{
				"vpc_id":            "module.aws_vpc.vpc_id",            // declared
				"nonexistent_field": "module.aws_vpc.does_not_exist_xy", // not declared
			},
		},
	}
	presetPaths := map[string]string{
		"aws_vpc": "aws/vpc",
	}

	issues := ValidateModuleWiring(blocks, presetPaths)
	require.Len(t, issues, 1, "exactly one missing-output issue expected")
	require.Equal(t, "unwired_output", issues[0].Code)
	require.Equal(t, "aws_alb.nonexistent_field", issues[0].Field)
	require.Contains(t, issues[0].Reason, "does_not_exist_xy")
}

func TestValidateModuleWiring_SkipsUnknownProducers(t *testing.T) {
	t.Parallel()

	// A wiring reference to a module not in presetPaths should be ignored,
	// not flagged. This protects synthetic test fixtures from false positives.
	blocks := []ModuleBlock{
		{Name: "aws_alb", Raw: map[string]string{"foo": "module.unknown_thing.bar"}},
	}
	require.Empty(t, ValidateModuleWiring(blocks, map[string]string{}))
}

func TestValidateNoModuleCycles_DetectsCycle(t *testing.T) {
	t.Parallel()

	// A -> B -> A (mutual references)
	blocks := []ModuleBlock{
		{Name: "a", Raw: map[string]string{"x": "module.b.x"}},
		{Name: "b", Raw: map[string]string{"y": "module.a.y"}},
	}
	issues := ValidateNoModuleCycles(blocks)
	require.Len(t, issues, 1)
	require.Equal(t, "module_cycle", issues[0].Code)
	require.Equal(t, "module_graph", issues[0].Field)

	// Locks the closing-edge hint so the diagnostic remains actionable.
	// Either edge of the 2-cycle qualifies; assert at least one lands in
	// the rendered "(e.g. ...)" form so the residual-graph walk can't be
	// silently regressed away.
	require.Regexp(t,
		`\(e\.g\. (a\.x -> module\.b\.x|b\.y -> module\.a\.y)\)`,
		issues[0].Reason,
		"cycle reason should pinpoint a closing edge for reviewer diagnostics")
	require.Contains(t, issues[0].Reason, "[a b]",
		"cycle reason should enumerate the deterministic-sorted module names")
}

// TestValidateNoModuleCycles_SelfLoopIgnored guards the explicit
// edge.Producer == edge.Consumer skip in the topo-sort. A module legitimately
// self-referencing (rare but valid in HCL) is not a cycle.
func TestValidateNoModuleCycles_SelfLoopIgnored(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{Name: "a", Raw: map[string]string{"x": "module.a.y"}},
	}
	require.Empty(t, ValidateNoModuleCycles(blocks),
		"self-references should not be classified as cycles")
}

func TestValidateNoModuleCycles_AllowsDAG(t *testing.T) {
	t.Parallel()

	// Linear chain: vpc -> alb -> rds (alb depends on vpc, rds depends on vpc).
	blocks := []ModuleBlock{
		{Name: "aws_vpc", Raw: map[string]string{}},
		{Name: "aws_alb", Raw: map[string]string{"vpc_id": "module.aws_vpc.vpc_id"}},
		{Name: "aws_rds", Raw: map[string]string{"vpc_id": "module.aws_vpc.vpc_id"}},
	}
	require.Empty(t, ValidateNoModuleCycles(blocks))
}

func TestValidateValueTypes_FlagsStringForNumber(t *testing.T) {
	t.Parallel()

	// gcp/gke declares node_count = number. Sending a non-numeric string must
	// fail conversion.
	moduleToVals := map[string]map[string]any{
		"gcp_gke": {"node_count": []string{"oops", "still-oops"}},
	}
	presetPaths := map[string]string{"gcp_gke": "gcp/gke"}

	issues := ValidateValueTypes(moduleToVals, presetPaths)
	require.NotEmpty(t, issues)
	found := false
	for _, iss := range issues {
		if iss.Field == "gcp_gke.node_count" {
			require.Equal(t, "invalid_type", iss.Code)
			require.Contains(t, iss.Reason, "number")
			// Lock the offending value into the issue payload so Riley
			// has the diagnostic it needs without re-resolving the IR.
			require.NotEmpty(t, iss.Value,
				"invalid_type issues must carry the offending value via issueValue()")
			require.Contains(t, iss.Value, "oops",
				"value should serialize the rejected input verbatim")
			found = true
		}
	}
	require.True(t, found, "expected gcp_gke.node_count invalid_type issue, got: %v", issues)
}

func TestValidateValueTypes_AcceptsValidValues(t *testing.T) {
	t.Parallel()

	// Sending an int for node_count should not flag.
	moduleToVals := map[string]map[string]any{
		"gcp_gke": {"node_count": 3},
	}
	presetPaths := map[string]string{"gcp_gke": "gcp/gke"}

	require.Empty(t, ValidateValueTypes(moduleToVals, presetPaths))
}

// TestComposeStackWithIssues_GreenStackHasNoGraphIssues pins the contract
// that a real-world stack composes cleanly under all module-graph
// validators. If a future preset rename or output removal slips through,
// this test fails before terraform plan ever runs.
func TestComposeStackWithIssues_GreenStackHasNoGraphIssues(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	r, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSALB, KeyAWSRDS},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC"},
		Cfg:          &Config{},
		Project:      "p",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	for _, iss := range r.Issues {
		require.NotEqual(t, "unwired_output", iss.Code, "unexpected unwired_output: %v", iss)
		require.NotEqual(t, "module_cycle", iss.Code, "unexpected module_cycle: %v", iss)
		require.NotEqual(t, "invalid_type", iss.Code, "unexpected invalid_type: %v", iss)
	}
}
