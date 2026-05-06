package composer

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestObservabilityMoves_KeyShapeIsZero is the regression tripwire for
// the destination/source `for_each` keying contract: every From and To
// literal must contain `["0"]`. If a future per-component module
// switches its alarm resource from singleton-list `for_each = { "0" =
// true }` to multi-instance `for_each = toset(var.ids)`, the
// destination key shape diverges and the moved block becomes a
// destroy+create. The drift design discussion is in
// docs/observability-consolidation.md (Risks).
func TestObservabilityMoves_KeyShapeIsZero(t *testing.T) {
	for k, refs := range observabilityMoves {
		for i, mv := range refs {
			from := mv.FromHCL()
			assert.Contains(t, from, `["0"]`,
				"observabilityMoves[%s][%d].FromHCL()=%q must contain string-keyed [\"0\"] (matches the legacy for_each = { for i in tolist(range(...)) : i => true } shape)",
				k, i, from)
			assert.Contains(t, mv.To, `["0"]`,
				"observabilityMoves[%s][%d].To=%q must contain string-keyed [\"0\"] (matches the per-component for_each = var.enable_observability ? { \"0\" = true } : {} shape)",
				k, i, mv.To)
		}
	}
}

// TestObservabilityMoves_DestinationCloudIsAWS asserts every entry's
// destination key resolves to an AWS component. The legacy aggregator
// is AWS-only; cross-cloud relocations would be nonsensical.
func TestObservabilityMoves_DestinationCloudIsAWS(t *testing.T) {
	for k := range observabilityMoves {
		assert.Equal(t, "aws", CloudFor(k),
			"observabilityMoves[%s] destination component is non-AWS; only AWS keys belong here (legacy aggregator is aws_cloudwatch_monitoring)",
			k)
	}
}

// TestObservabilityMoves_FromComponentIsAggregator asserts every move
// entry's source component is the legacy aggregator KeyAWSCloudWatchMonitoring.
// Catches a future entry that accidentally points at a different source
// module (which would silently fail to relocate the legacy alarm).
func TestObservabilityMoves_FromComponentIsAggregator(t *testing.T) {
	for k, refs := range observabilityMoves {
		for i, mv := range refs {
			assert.Equal(t, KeyAWSCloudWatchMonitoring, mv.FromComponent,
				"observabilityMoves[%s][%d].FromComponent=%q must be KeyAWSCloudWatchMonitoring (legacy aggregator)",
				k, i, mv.FromComponent)
		}
	}
}

// TestObservabilityMoves_DestinationsAreKnownComponentKeys catches
// typos / stale destination keys after a key rename or removal.
func TestObservabilityMoves_DestinationsAreKnownComponentKeys(t *testing.T) {
	known := make(map[ComponentKey]bool, len(AllComponentKeys))
	for _, k := range AllComponentKeys {
		known[k] = true
	}
	for k := range observabilityMoves {
		assert.True(t, known[k],
			"observabilityMoves[%s] is not in AllComponentKeys — stale or typo'd key",
			k)
	}
}

// TestObservabilityMovesCoversAllAggregatorAlarms asserts every
// `aws_cloudwatch_metric_alarm` resource in
// aws/cloudwatchmonitoring/main.tf has at least one matching
// observabilityMoves[k].From entry. Without this gate, adding a new
// legacy alarm without a corresponding move would silently leave that
// alarm orphaned in customer state once
// `disable_legacy_per_component_alarms = true` is flipped (a future
// The InsideOut backend PR).
func TestObservabilityMovesCoversAllAggregatorAlarms(t *testing.T) {
	resources := parseAggregatorAlarmResources(t)
	require.NotEmpty(t, resources,
		"expected to find at least one aws_cloudwatch_metric_alarm in aws/cloudwatchmonitoring/main.tf — HCL parse may have failed")

	froms := make(map[string]bool)
	for _, refs := range observabilityMoves {
		for _, mv := range refs {
			froms[mv.FromHCL()] = true
		}
	}

	for _, res := range resources {
		expected := WireRef(KeyAWSCloudWatchMonitoring, `aws_cloudwatch_metric_alarm.`+res+`["0"]`)
		assert.True(t, froms[expected],
			"aws_cloudwatch_metric_alarm.%s in aws/cloudwatchmonitoring/main.tf has no matching observabilityMoves entry rendering as %q — add an entry to pkg/composer/observability_moves.go so the relocation is wired",
			res, expected)
	}
}

// TestObservabilityMoves_NoUnknownAggregatorAlarms is the reverse of
// TestObservabilityMovesCoversAllAggregatorAlarms: every From entry in
// observabilityMoves must reference an alarm resource that actually
// exists in aws/cloudwatchmonitoring/main.tf. Catches stale move
// entries left behind after a legacy alarm is deleted.
func TestObservabilityMoves_NoUnknownAggregatorAlarms(t *testing.T) {
	resources := parseAggregatorAlarmResources(t)
	known := make(map[string]bool, len(resources))
	for _, r := range resources {
		known[r] = true
	}
	for k, refs := range observabilityMoves {
		for i, mv := range refs {
			// Extract resource name between `aws_cloudwatch_metric_alarm.` and `[`.
			require.Equal(t, KeyAWSCloudWatchMonitoring, mv.FromComponent,
				"observabilityMoves[%s][%d].FromComponent=%q does not match the legacy aggregator (KeyAWSCloudWatchMonitoring)",
				k, i, mv.FromComponent)
			const prefix = `aws_cloudwatch_metric_alarm.`
			require.True(t,
				strings.HasPrefix(mv.FromAddress, prefix),
				"observabilityMoves[%s][%d].FromAddress=%q does not match the legacy aggregator address shape (prefix=%q)",
				k, i, mv.FromAddress, prefix)
			rest := strings.TrimPrefix(mv.FromAddress, prefix)
			name := rest[:strings.IndexByte(rest, '[')]
			assert.True(t, known[name],
				"observabilityMoves[%s][%d].FromAddress references aggregator alarm %q which does not exist in aws/cloudwatchmonitoring/main.tf — stale entry, remove it",
				k, i, name)
		}
	}
}

// TestObservabilityMoves_PublicAccessor verifies the exported
// ObservabilityMoves(k) returns a copy (callers can mutate without
// polluting the package-level table) and matches the underlying entry.
func TestObservabilityMoves_PublicAccessor(t *testing.T) {
	got := ObservabilityMoves(KeyAWSSQS)
	require.Len(t, got, 1, "KeyAWSSQS should have exactly one move")
	assert.Equal(t, KeyAWSCloudWatchMonitoring, got[0].FromComponent)
	assert.Equal(t,
		`aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
		got[0].FromAddress)
	assert.Equal(t,
		`module.aws_cloudwatch_monitoring.aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
		got[0].FromHCL())

	// Mutate the returned slice and verify the package map is unchanged.
	got[0].FromAddress = "phantom"
	got2 := ObservabilityMoves(KeyAWSSQS)
	assert.NotEqual(t, "phantom", got2[0].FromAddress,
		"ObservabilityMoves must return a defensive copy")
}

// TestObservabilityMoves_PublicAccessor_UnknownKey returns nil for
// keys with no observability move history.
func TestObservabilityMoves_PublicAccessor_UnknownKey(t *testing.T) {
	assert.Nil(t, ObservabilityMoves(KeyAWSS3),
		"KeyAWSS3 has no legacy aggregator alarm; ObservabilityMoves should return nil")
}

// parseAggregatorAlarmResources HCL-parses
// aws/cloudwatchmonitoring/main.tf and returns the list of
// aws_cloudwatch_metric_alarm resource names declared there. Used by
// the cover/no-unknown drift gates above.
func parseAggregatorAlarmResources(t *testing.T) []string {
	t.Helper()
	path := repoRoot(t) + "/aws/cloudwatchmonitoring/main.tf"

	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	require.False(t, diags.HasErrors(),
		"failed to parse %s: %s", path, diags.Error())

	// Use PartialContent to enumerate resource blocks without forcing
	// schema validation for non-resource block types.
	content, _, diags := file.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "resource", LabelNames: []string{"type", "name"}},
		},
	})
	require.False(t, diags.HasErrors(),
		"PartialContent on %s: %s", path, diags.Error())

	var names []string
	for _, b := range content.Blocks {
		if b.Type != "resource" || len(b.Labels) != 2 {
			continue
		}
		if b.Labels[0] != "aws_cloudwatch_metric_alarm" {
			continue
		}
		names = append(names, b.Labels[1])
	}
	return names
}

// repoRoot returns the absolute path to the repository root by walking
// up from the calling test file's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	// file is /<repo>/pkg/composer/observability_moves_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
