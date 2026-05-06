package composer

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComposeStack_AllModuleRefsResolveToDeclaredBlocks is the runtime
// gate that catches semantic divergence between rendered `module "<X>"
// {}` block labels and the `module.<X>.…` traversals in argument
// expressions and `moved.from` attributes (issue #283). Where the static
// lint test (TestModuleReferenceLiteralsMatchComponentKeys) catches a
// hand-written literal sneaking back in, this test catches the case
// where the WireRef helpers themselves render the wrong key.
//
// It composes a representative selection (cloudwatch_monitoring + every
// driver in PricingDependencies[KeyAWSCloudWatchMonitoring] + the
// observability moves opt-in so `moved.from` references are exercised
// too), then walks the rendered main.tf and asserts every
// `module.<X>.` reference resolves to a declared block label. A
// terraform init against the composed root would fail if this assertion
// did.
func TestComposeStack_AllModuleRefsResolveToDeclaredBlocks(t *testing.T) {
	c := newTestClient()

	enabled := true
	comps := &Components{
		AWSVPC:                  "Private",
		AWSLambda:               &enabled,
		AWSRDS:                  &enabled,
		AWSALB:                  &enabled,
		AWSElastiCache:          &enabled,
		AWSAPIGateway:           &enabled,
		AWSSQS:                  &enabled,
		AWSCloudWatchMonitoring: &enabled,
	}

	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSLambda,
			KeyAWSRDS,
			KeyAWSALB,
			KeyAWSElastiCache,
			KeyAWSAPIGateway,
			KeyAWSSQS,
			KeyAWSCloudWatchMonitoring,
		},
		Comps:                  comps,
		Cfg:                    &Config{Region: "us-east-1"},
		Project:                "test-283",
		Region:                 "us-east-1",
		EmitObservabilityMoves: true,
	})
	require.NoError(t, err)
	mainTF, ok := out["/main.tf"]
	require.True(t, ok, "expected /main.tf in composed output")
	body := string(mainTF)

	declRe := regexp.MustCompile(`module\s+"([^"]+)"\s*\{`)
	declared := map[string]bool{}
	for _, m := range declRe.FindAllStringSubmatch(body, -1) {
		declared[m[1]] = true
	}
	require.NotEmpty(t, declared, "expected at least one declared module block in:\n%s", body)
	require.True(t, declared[string(KeyAWSCloudWatchMonitoring)],
		"sanity: composed root must declare module %q (selection includes the aggregator); declared=%v",
		KeyAWSCloudWatchMonitoring, declared)

	refRe := regexp.MustCompile(`module\.([a-z][a-z0-9_]*)\.`)
	for _, m := range refRe.FindAllStringSubmatch(body, -1) {
		ref := m[1]
		assert.True(t, declared[ref],
			"composed main.tf references module.%s.… but no `module \"%s\" {}` block is declared. "+
				"This is the #283 class of bug — a wire literal or WireRef call rendered an identifier that doesn't match the declared block label. "+
				"declared=%v",
			ref, ref, declared)
	}
}

// TestComposeStack_ObservabilityWireMatchesAggregatorBlock is the
// minimal end-to-end repro of issue #283: select the aggregator + one
// observability driver, render main.tf, and assert the
// `alarm_topic_arn = module.<X>.sns_topic_arn` reference's <X> matches
// the rendered `module "<X>" {}` block label. This test fails red on
// `main` before the WireRef migration and green after.
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
	body := string(out["/main.tf"])

	expectedDecl := `module "` + string(KeyAWSCloudWatchMonitoring) + `"`
	require.Contains(t, body, expectedDecl,
		"expected aggregator block %q in composed main.tf", expectedDecl)

	expectedRef := WireRef(KeyAWSCloudWatchMonitoring, "sns_topic_arn")
	require.Contains(t, body, expectedRef,
		"expected SQS module to receive alarm_topic_arn = %s; if this fails the wire reference does not match the aggregator's block label (#283)",
		expectedRef)
}
