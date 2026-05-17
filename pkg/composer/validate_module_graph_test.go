package composer

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
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

// TestValidateNoModuleCycles_LocalsIndirectionBreaksCycle pins the
// #601 Option G contract at the validator layer: a 2-module graph
// where A→B is wired via a direct `module.B.X` reference and B→A is
// wired via `local.foo` (which the composer separately emits as
// `local foo = module.A.Y`) must NOT be flagged as a module_cycle.
//
// extractWiringEdges (validate_module_graph.go) intentionally only
// matches `module.X.Y` traversals — the locals layer is the cycle-
// break mechanism. This test would fail if a future refactor broadens
// the regex to also match `local.X` or starts inspecting a separate
// locals registry; both would re-introduce the regression #602
// deliberately deferred.
func TestValidateNoModuleCycles_LocalsIndirectionBreaksCycle(t *testing.T) {
	t.Parallel()

	// A consumes B directly (module-ref); B consumes A via a local-ref
	// (the composer emits `local x = module.a.out` separately, but the
	// validator only sees module blocks).
	blocks := []ModuleBlock{
		{Name: "a", Raw: map[string]string{"input_from_b": "module.b.out"}},
		{Name: "b", Raw: map[string]string{"input_from_a": "local.a_out"}},
	}
	require.Empty(t, ValidateNoModuleCycles(blocks),
		"local.X traversals must not register as wiring edges; the locals layer "+
			"is the #601 cycle-break mechanism. If this fails, extractWiringEdges "+
			"has been broadened to inspect locals — the back-edge wiring contract is broken.")
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
			// Lock the offending value into the issue payload so the
			// interactive agent has the diagnostic it needs without
			// re-resolving the IR.
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

// TestValidateNoUnionCycles_DetectsCrossTierCycle pins that a cycle
// spanning a preset module and a flat imported resource is reported
// as wiring_cycle (not module_cycle), with the closing-edge hint
// rendered in module/resource form so reviewers can break the cycle.
func TestValidateNoUnionCycles_DetectsCrossTierCycle(t *testing.T) {
	t.Parallel()

	// Module aws_lambda consumes resource aws_sqs_queue.dlq.arn;
	// resource aws_sqs_queue.dlq consumes module.aws_lambda.role_arn.
	blocks := []ModuleBlock{
		{Name: "aws_lambda", Raw: map[string]string{"dlq_arn": "aws_sqs_queue.dlq.arn"}},
	}
	irs := []imported.ImportedResource{
		{
			Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.dlq", ImportID: "x"},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{"redrive_role_arn": RawExpr{Expr: "module.aws_lambda.role_arn"}},
		},
	}

	issues := ValidateNoUnionCycles(blocks, irs)
	require.Len(t, issues, 1)
	require.Equal(t, "wiring_cycle", issues[0].Code)
	require.Equal(t, "module_graph", issues[0].Field)
	require.Contains(t, issues[0].Reason, "module.aws_lambda")
	require.Contains(t, issues[0].Reason, "aws_sqs_queue.dlq")
	// Closing-edge hint locks the rendering format `(e.g. <consumerSlot>
	// -> <producer>.<attr>)`. Either edge of the 2-cycle qualifies; both
	// shapes are pinned so an argument-swap mutation surfaces.
	require.Regexp(t,
		`\(e\.g\. (imported\.aws_sqs_queue\.dlq\.redrive_role_arn -> module\.aws_lambda\.role_arn|aws_lambda\.dlq_arn -> aws_sqs_queue\.dlq\.arn)\)`,
		issues[0].Reason,
		"closing-edge hint must reference both nodes with kind-specific prefixes")
}

// TestValidateNoUnionCycles_PureModuleCycleSkipped pins the deferral
// contract: a pure-module cycle is left to ValidateNoModuleCycles so
// the canonical `module_cycle` code is emitted exactly once instead
// of double-reporting.
func TestValidateNoUnionCycles_PureModuleCycleSkipped(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{Name: "a", Raw: map[string]string{"x": "module.b.x"}},
		{Name: "b", Raw: map[string]string{"y": "module.a.y"}},
	}
	require.Empty(t, ValidateNoUnionCycles(blocks, nil),
		"pure-module cycle must be deferred to ValidateNoModuleCycles")
}

// TestValidateNoUnionCycles_DAGNoIssue guards the happy path: a mixed
// module + imported-resource graph with a strict topological order
// produces no cycles.
func TestValidateNoUnionCycles_DAGNoIssue(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{Name: "aws_lambda", Raw: map[string]string{"dlq_arn": "aws_sqs_queue.dlq.arn"}},
	}
	irs := []imported.ImportedResource{
		{
			Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.dlq", ImportID: "x"},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{},
		},
	}
	require.Empty(t, ValidateNoUnionCycles(blocks, irs))
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
	// Positive guard: the compose actually produced the root files we
	// expect — a "Liar Test" where the validators silently no-op'd
	// would yield empty Files and no bad codes, falsely passing.
	require.NotEmpty(t, r.Files["/main.tf"], "compose must emit /main.tf")
	require.NotEmpty(t, r.Files["/variables.tf"], "compose must emit /variables.tf")
	for _, iss := range r.Issues {
		require.NotEqual(t, "unwired_output", iss.Code, "unexpected unwired_output: %v", iss)
		require.NotEqual(t, "module_cycle", iss.Code, "unexpected module_cycle: %v", iss)
		require.NotEqual(t, "wiring_cycle", iss.Code, "unexpected wiring_cycle: %v", iss)
		require.NotEqual(t, "invalid_type", iss.Code, "unexpected invalid_type: %v", iss)
		require.NotEqual(t, "dangling_resource_ref", iss.Code, "unexpected dangling_resource_ref: %v", iss)
	}
}

// TestComposeStack_NoCycleWithCloudWatchMonitoringAndConsumers regresses
// the bug surfaced by reliable session sess_v2_T8vvrDtATMBN where
// ValidateNoModuleCycles reported [aws_alb aws_apigateway aws_cloudfront
// aws_cloudwatch_monitoring aws_elasticache aws_lambda aws_rds] stuck due
// to the monitoring<->consumer 2-cycles formed by the legacy aggregator-
// side wiring (instance_ids/rds_instance_ids/alb_arn_suffixes/sqs_queue_arns)
// combined with the per-component observability wiring
// (alarm_topic_arn = module.aws_cloudwatch_monitoring.sns_topic_arn).
//
// Fix: when any per-component observability consumer is in the stack,
// the cwm aggregator drops its back-edge wiring and flips
// disable_legacy_per_component_alarms = true. The forward-edge stays so
// per-component alarms continue to notify via the shared SNS topic.
// Issue #285.
func TestComposeStack_NoCycleWithCloudWatchMonitoringAndConsumers(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	r, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Project: "demo",
		Region:  "us-east-1",
		Cloud:   "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSRDS,
			KeyAWSALB,
			KeyAWSLambda,
			KeyAWSAPIGateway,
			KeyAWSElastiCache,
			KeyAWSCloudfront,
			KeyAWSCloudWatchMonitoring,
		},
		Comps: &Components{
			Cloud:                   "AWS",
			AWSVPC:                  "Private VPC",
			AWSRDS:                  ptrBool(true),
			AWSALB:                  ptrBool(true),
			AWSLambda:               ptrBool(true),
			AWSAPIGateway:           ptrBool(true),
			AWSElastiCache:          ptrBool(true),
			AWSCloudFront:           ptrBool(true),
			AWSCloudWatchMonitoring: ptrBool(true),
		},
		Cfg: &Config{},
	})
	require.NoError(t, err, "compose must succeed for cwm + consumers stack")
	require.NotNil(t, r)

	for _, iss := range r.Issues {
		require.NotEqual(t, "module_cycle", iss.Code,
			"no module_cycle expected for cwm + consumers stack: %v", iss)
		require.NotEqual(t, "wiring_cycle", iss.Code,
			"no wiring_cycle expected for cwm + consumers stack: %v", iss)
	}

	mainTF := string(r.Files["/main.tf"])
	require.NotEmpty(t, mainTF, "compose must emit /main.tf")

	// Aggregator-side: legacy alarms disabled, back-edges absent.
	// Bare-RHS substrings (no whitespace alignment) so a renderer alignment
	// change can't make these checks vacuously pass.
	require.Regexp(t,
		regexp.MustCompile(`(?m)^\s*disable_legacy_per_component_alarms\s*=\s*true\s*$`),
		mainTF,
		"legacy alarms must be disabled when per-component observability is wired")
	require.NotContains(t, mainTF, "module.aws_bastion.bastion_instance_id",
		"back-edge from cwm to bastion must not render (#285)")
	require.NotContains(t, mainTF, "module.aws_rds.instance_id",
		"back-edge from cwm to rds must not render (#285)")
	require.NotContains(t, mainTF, "module.aws_alb.alb_arn_suffix",
		"back-edge from cwm to alb must not render (#285)")
	require.NotContains(t, mainTF, "module.aws_sqs.queue_arn",
		"back-edge from cwm to sqs must not render (#285)")

	// Forward-edge: per-component alarms still notify via the cwm SNS topic.
	require.Contains(t, mainTF, "alarm_topic_arn      = module.aws_cloudwatch_monitoring.sns_topic_arn",
		"forward-edge alarm_topic_arn must still render so per-component alarms notify")
	require.Regexp(t,
		regexp.MustCompile(`(?m)^\s*enable_observability\s*=\s*true\s*$`),
		mainTF,
		"forward-edge enable_observability must still render")

	// Cross-check: every emitted module.<X>.… reference resolves to a
	// declared module block (defense against #283-class regressions).
	assertComposedRefsResolveToBlocks(t, mainTF, KeyAWSCloudWatchMonitoring)
}

// TestValidateNoModuleCycles_PinsCWMConsumer2Cycle locks the lower-level
// validator behavior on the specific 2-cycle shape (cwm <-> rds) so a
// regression to the wiring layer that re-introduces the back-edge
// surfaces here as well. Independent of the wiring fix — this is a
// validator-shape pin. Issue #285.
func TestValidateNoModuleCycles_PinsCWMConsumer2Cycle(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{
			Name: "aws_cloudwatch_monitoring",
			Raw: map[string]string{
				"rds_instance_ids": "[module.aws_rds.instance_id]",
			},
		},
		{
			Name: "aws_rds",
			Raw: map[string]string{
				"alarm_topic_arn": "module.aws_cloudwatch_monitoring.sns_topic_arn",
			},
		},
	}
	issues := ValidateNoModuleCycles(blocks)
	require.Len(t, issues, 1)
	require.Equal(t, "module_cycle", issues[0].Code)
	// Assert each module name independently rather than the bracketed
	// slice form — decouples this test from Go's default slice-print
	// formatting so a cosmetic reformatter change doesn't break it.
	require.Contains(t, issues[0].Reason, "aws_cloudwatch_monitoring",
		"cycle reason should reference the cwm module")
	require.Contains(t, issues[0].Reason, "aws_rds",
		"cycle reason should reference the rds module")
}
