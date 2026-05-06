package composer

import (
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertComposedRefsResolveToBlocks composes a stack with the given
// selection and confirms every `module.<X>.…` traversal in the rendered
// main.tf — both inside argument expressions and inside `moved.from`
// attributes — resolves to a declared `module "<X>" {}` block.
//
// This is the load-bearing assertion for the runtime gate against
// issue #283: where the static lint catches hand-written or
// dynamically-composed `module.<X>.…` literals at compile time, this
// catches the case where the WireRef helpers themselves render the
// wrong key. Either of the two gates in isolation could be evaded;
// together they close both axes.
//
// The test also asserts at least one cross-module wire is present —
// otherwise a regression that silently deleted DefaultWiring's body
// would pass via vacuous truth.
func assertComposedRefsResolveToBlocks(t *testing.T, body string, mustDeclare ComponentKey) {
	t.Helper()

	declRe := regexp.MustCompile(`module\s+"([^"]+)"\s*\{`)
	declared := map[string]bool{}
	for _, m := range declRe.FindAllStringSubmatch(body, -1) {
		declared[m[1]] = true
	}
	require.NotEmpty(t, declared, "expected at least one declared module block in:\n%s", body)
	require.True(t, declared[string(mustDeclare)],
		"sanity: composed root must declare module %q (selection includes the aggregator); declared=%v",
		mustDeclare, declared)

	refRe := regexp.MustCompile(`module\.([a-z][a-z0-9_]*)\.`)
	matches := refRe.FindAllStringSubmatch(body, -1)
	require.NotEmpty(t, matches,
		"composed main.tf contains zero `module.<X>.…` references — DefaultWiring may have been silently emptied; declared=%v",
		declared)

	for _, m := range matches {
		ref := m[1]
		assert.True(t, declared[ref],
			"composed main.tf references module.%s.… but no `module \"%s\" {}` block is declared. "+
				"This is the #283 class of bug — a wire literal or WireRef call rendered an identifier that doesn't match the declared block label. "+
				"declared=%v",
			ref, ref, declared)
	}
}

// nonComputeDrivers returns drivers that aren't in any of the supplied
// compute-key registries, so each per-architecture subtest can append
// its compute keys without conflict with ValidateComputeExclusivity.
//
// Reads from the exported AWSServerlessKeys / AWSContainerKeys /
// GCPServerlessKeys / GCPContainerKeys registries in validate.go so
// the test stays in lockstep with ValidateComputeExclusivity — adding
// a new compute key in one place automatically updates the other.
func nonComputeDrivers(drivers []ComponentKey, registries ...[]ComponentKey) []ComponentKey {
	out := make([]ComponentKey, 0, len(drivers))
	for _, k := range drivers {
		isCompute := false
		for _, reg := range registries {
			if slices.Contains(reg, k) {
				isCompute = true
				break
			}
		}
		if !isCompute {
			out = append(out, k)
		}
	}
	return out
}

// TestComposeStack_AllAWSObservabilityWiresResolveToDeclaredBlocks runs
// the runtime gate over every AWS observability wire that
// PricingDependencies[KeyAWSCloudWatchMonitoring] declares. The
// driver list is iterated mechanically so a new entry there is
// automatically covered. Compute architectures conflict per
// ValidateComputeExclusivity, so the test partitions into Lambda /
// container subtests; together they exercise every PricingDependencies
// driver plus the EmitObservabilityMoves opt-in (so `moved.from`
// references are also walked).
func TestComposeStack_AllAWSObservabilityWiresResolveToDeclaredBlocks(t *testing.T) {
	c := newTestClient()
	enabled := true
	intel := "Intel"

	// Sanity-pin against accidental empty PricingDependencies.
	require.NotEmpty(t, PricingDependencies[KeyAWSCloudWatchMonitoring],
		"PricingDependencies[KeyAWSCloudWatchMonitoring] must be non-empty for the runtime gate to exercise any wires")

	nonCompute := nonComputeDrivers(
		PricingDependencies[KeyAWSCloudWatchMonitoring],
		AWSServerlessKeys, AWSContainerKeys,
	)

	type variant struct {
		name        string
		computeKeys []ComponentKey
		comps       func(c *Components)
	}

	variants := []variant{
		{
			name:        "lambda_only",
			computeKeys: []ComponentKey{KeyAWSLambda},
			comps:       func(c *Components) { c.AWSLambda = &enabled },
		},
		{
			name:        "eks_ecs_ec2_combo",
			computeKeys: []ComponentKey{KeyAWSEKS, KeyAWSECS, KeyAWSEC2},
			comps: func(c *Components) {
				c.AWSEKS = &enabled
				c.AWSECS = &enabled
				c.AWSEC2 = intel
			},
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			comps := &Components{
				AWSVPC:                  "Private",
				AWSALB:                  &enabled,
				AWSAPIGateway:           &enabled,
				AWSRDS:                  &enabled,
				AWSElastiCache:          &enabled,
				AWSDynamoDB:             &enabled,
				AWSOpenSearch:           &enabled,
				AWSSQS:                  &enabled,
				AWSMSK:                  &enabled,
				AWSBastion:              &enabled,
				AWSCloudWatchMonitoring: &enabled,
			}
			v.comps(comps)

			selected := append([]ComponentKey{KeyAWSVPC, KeyAWSCloudWatchMonitoring}, nonCompute...)
			selected = append(selected, v.computeKeys...)

			out, err := c.ComposeStack(ComposeStackOpts{
				Cloud:                  "aws",
				SelectedKeys:           selected,
				Comps:                  comps,
				Cfg:                    &Config{Region: "us-east-1"},
				Project:                "test-283-aws-" + v.name,
				Region:                 "us-east-1",
				EmitObservabilityMoves: true,
			})
			require.NoError(t, err)
			mainTF, ok := out["/main.tf"]
			require.True(t, ok, "expected /main.tf in composed output")

			assertComposedRefsResolveToBlocks(t, string(mainTF), KeyAWSCloudWatchMonitoring)
		})
	}
}

// TestComposeStack_AllGCPObservabilityWiresResolveToDeclaredBlocks
// mirrors the AWS gate over the GCP wire surface. DefaultWiring's GCP
// arm has eight WireRef call sites (network_self_link x6,
// connector_id x2, security_policy_id, notification_channels) plus the
// observability bind on every PricingDependencies[KeyGCPCloudMonitoring]
// driver, none of which were exercised by an end-to-end runtime gate
// before this test landed. A future typo introducing
// `WireRef("gcp_cloudmonitoring", ...)` (missing underscore — exactly
// the #283 shape) would ship green without it. Compute architectures
// conflict per ValidateComputeExclusivity, so partition into
// serverless / container subtests.
func TestComposeStack_AllGCPObservabilityWiresResolveToDeclaredBlocks(t *testing.T) {
	c := newTestClient()
	enabled := true
	intel := "Intel"

	require.NotEmpty(t, PricingDependencies[KeyGCPCloudMonitoring],
		"PricingDependencies[KeyGCPCloudMonitoring] must be non-empty for the runtime gate to exercise any wires")

	nonCompute := nonComputeDrivers(
		PricingDependencies[KeyGCPCloudMonitoring],
		GCPServerlessKeys, GCPContainerKeys,
	)

	type variant struct {
		name        string
		computeKeys []ComponentKey
		comps       func(c *Components)
	}

	variants := []variant{
		{
			name:        "cloud_run_and_functions",
			computeKeys: []ComponentKey{KeyGCPCloudFunctions, KeyGCPCloudRun},
			comps: func(c *Components) {
				c.GCPCloudFunctions = &enabled
				c.GCPCloudRun = &enabled
			},
		},
		{
			name:        "gke_only",
			computeKeys: []ComponentKey{KeyGCPGKE},
			comps:       func(c *Components) { c.GCPGKE = &enabled },
		},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			comps := &Components{
				GCPVPC:             &enabled,
				GCPCompute:         intel,
				GCPLoadbalancer:    &enabled,
				GCPCloudArmor:      &enabled, // exercises security_policy_id wire
				GCPCloudSQL:        &enabled,
				GCPMemorystore:     &enabled,
				GCPAPIGateway:      &enabled,
				GCPPubSub:          &enabled,
				GCPFirestore:       &enabled,
				GCPBastion:         &enabled,
				GCPCloudMonitoring: &enabled,
			}
			v.comps(comps)

			selected := append([]ComponentKey{KeyGCPVPC, KeyGCPCloudArmor, KeyGCPCloudMonitoring}, nonCompute...)
			selected = append(selected, v.computeKeys...)

			out, err := c.ComposeStack(ComposeStackOpts{
				Cloud:        "gcp",
				SelectedKeys: selected,
				Comps:        comps,
				Cfg:          &Config{Region: "us-central1"},
				Project:      "test-283-gcp-" + v.name,
				Region:       "us-central1",
				GCPProjectID: "test-283-gcp-projid",
			})
			require.NoError(t, err)
			mainTF, ok := out["/main.tf"]
			require.True(t, ok, "expected /main.tf in composed output")

			assertComposedRefsResolveToBlocks(t, string(mainTF), KeyGCPCloudMonitoring)
		})
	}
}

// TestComposeStack_ObservabilityWireMatchesAggregatorBlock is the
// minimal end-to-end repro of issue #283. The expected literal is
// pinned as a hardcoded string (NOT computed via WireRef) so this
// assertion is independent of the helper under test — if WireRef ever
// drifts to render a different identifier, this test catches it. The
// other runtime gate (assertComposedRefsResolveToBlocks) proves the
// block-label/ref consistency property; this test pins the canonical
// rendered value.
func TestComposeStack_ObservabilityWireMatchesAggregatorBlock(t *testing.T) {
	c := newTestClient()
	enabled := true
	comps := &Components{
		AWSVPC:                  "Private",
		AWSSQS:                  &enabled,
		AWSCloudWatchMonitoring: &enabled,
	}
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC, KeyAWSSQS, KeyAWSCloudWatchMonitoring,
		},
		Comps:   comps,
		Cfg:     &Config{Region: "us-east-1"},
		Project: "test-283",
		Region:  "us-east-1",
	})
	require.NoError(t, err)
	require.Contains(t, out, "/main.tf",
		"composed output must contain /main.tf — check that the composer's emit path hasn't been renamed")
	body := string(out["/main.tf"])

	// Pinned canonical strings — NOT WireRef-derived. If KeyAWSCloudWatchMonitoring
	// ever renames or WireRef rewrites identifiers, this test fails red so a
	// human reviews the change. Both halves of the #283 bug are pinned: the
	// declared block label AND the wire reference.
	require.Contains(t, body, `module "aws_cloudwatch_monitoring"`,
		"composed root must declare `module \"aws_cloudwatch_monitoring\" {}` (the canonical aggregator block label)")
	require.Contains(t, body, `module.aws_cloudwatch_monitoring.sns_topic_arn`,
		"composed SQS module must reference the canonical aggregator wire `module.aws_cloudwatch_monitoring.sns_topic_arn`. "+
			"If this fails, the wire reference does not match the declared block label — the #283 bug shape.")
}
