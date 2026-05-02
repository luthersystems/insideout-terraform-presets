package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultWiring_ObservabilityAWS_NotSelected verifies the post-switch
// loop adds nothing when aws_cloudwatchmonitoring is not selected.
func TestDefaultWiring_ObservabilityAWS_NotSelected(t *testing.T) {
	selected := map[ComponentKey]bool{KeyAWSSQS: true}
	wi := DefaultWiring(selected, KeyAWSSQS, &Components{})
	_, hasArn := wi.RawHCL["alarm_topic_arn"]
	_, hasGate := wi.RawHCL["enable_observability"]
	assert.False(t, hasArn,
		"alarm_topic_arn must not be wired when aws_cloudwatchmonitoring is not selected")
	assert.False(t, hasGate,
		"enable_observability must not be wired when aws_cloudwatchmonitoring is not selected")
}

// TestDefaultWiring_ObservabilityAWS_SelectedDriver verifies wiring fires
// when both the aggregator and a driver from PricingDependencies are
// selected.
func TestDefaultWiring_ObservabilityAWS_SelectedDriver(t *testing.T) {
	selected := map[ComponentKey]bool{
		KeyAWSCloudWatchMonitoring: true,
		KeyAWSSQS:                  true,
	}
	wi := DefaultWiring(selected, KeyAWSSQS, &Components{})
	assert.Equal(t, "module.aws_cloudwatchmonitoring.sns_topic_arn",
		wi.RawHCL["alarm_topic_arn"],
		"SQS should receive the SNS topic ARN from the aggregator when both are selected")
	assert.Equal(t, "true", wi.RawHCL["enable_observability"],
		"SQS should be opted into observability by default when the aggregator is selected")
}

// TestDefaultWiring_ObservabilityAWS_NonDriver verifies non-driver
// components (e.g. KeyAWSS3 isn't in the AWS observability driver list)
// don't get the wiring even when the aggregator is selected.
func TestDefaultWiring_ObservabilityAWS_NonDriver(t *testing.T) {
	require.NotContains(t, PricingDependencies[KeyAWSCloudWatchMonitoring], KeyAWSS3,
		"S3 should not be in the AWS CloudWatch monitoring driver list (precondition)")
	selected := map[ComponentKey]bool{
		KeyAWSCloudWatchMonitoring: true,
		KeyAWSS3:                   true,
	}
	wi := DefaultWiring(selected, KeyAWSS3, &Components{})
	_, hasArn := wi.RawHCL["alarm_topic_arn"]
	assert.False(t, hasArn,
		"S3 must not receive observability wiring (it isn't in PricingDependencies[KeyAWSCloudWatchMonitoring])")
}

// TestDefaultWiring_ObservabilityAWS_AggregatorItself verifies the
// aggregator never wires itself.
func TestDefaultWiring_ObservabilityAWS_AggregatorItself(t *testing.T) {
	selected := map[ComponentKey]bool{KeyAWSCloudWatchMonitoring: true}
	wi := DefaultWiring(selected, KeyAWSCloudWatchMonitoring, &Components{})
	_, hasArn := wi.RawHCL["alarm_topic_arn"]
	assert.False(t, hasArn,
		"aws_cloudwatchmonitoring must not receive its own alarm_topic_arn wiring")
}

// TestDefaultWiring_ObservabilityGCP_SelectedDriver verifies GCP wiring.
func TestDefaultWiring_ObservabilityGCP_SelectedDriver(t *testing.T) {
	selected := map[ComponentKey]bool{
		KeyGCPCloudMonitoring: true,
		KeyGCPMemorystore:     true,
	}
	wi := DefaultWiring(selected, KeyGCPMemorystore, &Components{})
	assert.Equal(t, "module.gcp_cloud_monitoring.notification_channels",
		wi.RawHCL["notification_channels"],
		"Memorystore should receive the notification_channels output from the aggregator")
	assert.Equal(t, "true", wi.RawHCL["enable_observability"])
}

// TestDefaultWiring_ObservabilityGCP_AggregatorItself verifies the GCP
// aggregator never wires itself.
func TestDefaultWiring_ObservabilityGCP_AggregatorItself(t *testing.T) {
	selected := map[ComponentKey]bool{KeyGCPCloudMonitoring: true}
	wi := DefaultWiring(selected, KeyGCPCloudMonitoring, &Components{})
	_, hasCh := wi.RawHCL["notification_channels"]
	assert.False(t, hasCh,
		"gcp_cloud_monitoring must not receive its own notification_channels wiring")
}

// TestComposeStack_EmitsObservabilityMovedBlocks_AWS composes a stack
// with aws_vpc + aws_sqs + aws_cloudwatchmonitoring and asserts the
// emitted root main.tf contains the moved {} block relocating the SQS
// alarm into the per-component module. End-to-end exercise of C3 emit +
// C4 moves table + C5 compose-loop population.
func TestComposeStack_EmitsObservabilityMovedBlocks_AWS(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSSQS,
			KeyAWSCloudWatchMonitoring,
		},
		Comps:   &Components{},
		Cfg:     &Config{Region: "us-east-1"},
		Project: "test-obs-204",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	mainTF, ok := out["/main.tf"]
	require.True(t, ok, "expected /main.tf in composed output")

	body := string(mainTF)
	assert.Contains(t, body,
		`from = module.aws_cloudwatchmonitoring.aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
		"composed root main.tf must contain SQS-source moved.from")
	assert.Contains(t, body,
		`to   = module.aws_sqs.aws_cloudwatch_metric_alarm.backlog["0"]`,
		"composed root main.tf must contain SQS-destination moved.to")
}

// TestComposeStack_NoMovedBlocksWithoutAggregator verifies the moved {}
// blocks are NOT emitted when the aggregator is absent — i.e. selecting
// SQS alone doesn't drag in observability moves that would dangle.
func TestComposeStack_NoMovedBlocksWithoutAggregator(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSSQS},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test-obs-204",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	mainTF, ok := out["/main.tf"]
	require.True(t, ok)

	body := string(mainTF)
	assert.NotContains(t, body, "moved {",
		"composed root main.tf must not contain moved {} blocks when the aggregator is absent")
}

// TestComposeStack_FilteredWiring_NoUnknownArguments verifies the
// compose-loop filter actually drops wiring entries the destination
// module doesn't declare. Composes a stack with cloudwatchmonitoring +
// SQS — SQS doesn't yet have an alarm_topic_arn variable (lands in C7)
// — and asserts that the SQS module block in the emitted root does NOT
// contain `alarm_topic_arn = ...`. Without the filter, terraform plan
// would fail with "An argument named alarm_topic_arn is not expected
// here."
//
// This test will need updating once C7 lands the variable
// declarations; at that point the filter becomes a no-op for SQS.
func TestComposeStack_FilteredWiring_NoUnknownArguments(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSSQS,
			KeyAWSCloudWatchMonitoring,
		},
		Comps:   &Components{},
		Cfg:     &Config{Region: "us-east-1"},
		Project: "test-obs-204",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	body := string(out["/main.tf"])
	// Find the aws_sqs module block boundaries.
	startIdx := strings.Index(body, `module "aws_sqs"`)
	require.True(t, startIdx >= 0, "expected aws_sqs module in composed output")
	// Use the next module block (or end of file) as the upper bound.
	rest := body[startIdx+len(`module "aws_sqs"`):]
	end := strings.Index(rest, `module "`)
	if end < 0 {
		end = len(rest)
	}
	sqsBlock := rest[:end]
	// Until aws/sqs declares alarm_topic_arn, the filter must keep it
	// out of the emitted block. Adjust this in C7 (one-line flip).
	assert.NotContains(t, sqsBlock, "alarm_topic_arn",
		"aws_sqs module block must not contain alarm_topic_arn until C7 lands the variable declaration")
}
