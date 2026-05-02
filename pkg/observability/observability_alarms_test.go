package observability

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// TestObservabilitySpecMatchesEmittedAlarms is the C9 forward-direction
// drift gate: every (component, metric) declared in alarmedAWSMetrics
// or alarmedGCPMetrics must have a matching alarm resource in the
// referenced module's observability.tf.
//
// Forward direction protects against the most common regression:
// deleting an HCL alarm but forgetting to flip the catalog Alarmed
// flag → the gate trips. The reverse direction (HCL alarm with no
// matching catalog spec) is intentionally NOT enforced here — several
// shipped alarms target metrics outside the ported reliable catalog
// (api_gateway, firestore, gke, bastion on GCP) and reverse-drift is
// tracked under #204 follow-up once the catalog is rebased.
func TestObservabilitySpecMatchesEmittedAlarms(t *testing.T) {
	root := repoRoot(t)
	for k, author := range alarmedAWSMetrics {
		t.Run("aws/"+string(k), func(t *testing.T) {
			path := filepath.Join(root, author.Module, "observability.tf")
			haveByMetric := parseAWSAlarmMetricNames(t, path)
			require.NotEmpty(t, haveByMetric,
				"expected at least one aws_cloudwatch_metric_alarm in %s — HCL parse may have failed", path)
			for _, m := range author.Metrics {
				assert.True(t, haveByMetric[m],
					"alarmedAWSMetrics[%s] declares metric %q but no aws_cloudwatch_metric_alarm with metric_name=%q exists in %s — flip the catalog entry or add the alarm",
					k, m, m, path)
			}
		})
	}
	for k, author := range alarmedGCPMetrics {
		t.Run("gcp/"+string(k), func(t *testing.T) {
			path := filepath.Join(root, author.Module, "observability.tf")
			filters := parseGCPAlertFilters(t, path)
			require.NotEmpty(t, filters,
				"expected at least one google_monitoring_alert_policy in %s — HCL parse may have failed", path)
			for _, m := range author.Metrics {
				needle := `metric.type="` + m + `"`
				found := false
				for _, f := range filters {
					if strings.Contains(f, needle) {
						found = true
						break
					}
				}
				assert.True(t, found,
					"alarmedGCPMetrics[%s] declares metric.type %q but no google_monitoring_alert_policy filter in %s contains %q — flip the catalog entry or add the alert policy",
					k, m, path, needle)
			}
		})
	}
}

// TestAlarmedMetricsKeysAreKnown catches typos / stale keys after a
// component rename or removal.
func TestAlarmedMetricsKeysAreKnown(t *testing.T) {
	known := make(map[composer.ComponentKey]bool, len(composer.AllComponentKeys))
	for _, k := range composer.AllComponentKeys {
		known[k] = true
	}
	for k := range alarmedAWSMetrics {
		assert.True(t, known[k],
			"alarmedAWSMetrics[%s] is not in AllComponentKeys — stale or typo'd key",
			k)
	}
	for k := range alarmedGCPMetrics {
		assert.True(t, known[k],
			"alarmedGCPMetrics[%s] is not in AllComponentKeys — stale or typo'd key",
			k)
	}
}

// TestAlarmedMetricsCloudMatchesKey ensures alarmedAWSMetrics carries
// only AWS keys and alarmedGCPMetrics only GCP keys.
func TestAlarmedMetricsCloudMatchesKey(t *testing.T) {
	for k := range alarmedAWSMetrics {
		assert.Equal(t, "aws", composer.CloudFor(k),
			"alarmedAWSMetrics[%s] is non-AWS", k)
	}
	for k := range alarmedGCPMetrics {
		assert.Equal(t, "gcp", composer.CloudFor(k),
			"alarmedGCPMetrics[%s] is non-GCP", k)
	}
}

// parseAWSAlarmMetricNames HCL-parses the given observability.tf and
// returns the set of metric_name attribute values from every
// aws_cloudwatch_metric_alarm resource block.
func parseAWSAlarmMetricNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	require.False(t, diags.HasErrors(), "parse %s: %s", path, diags.Error())

	content, _, diags := file.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "resource", LabelNames: []string{"type", "name"}},
		},
	})
	require.False(t, diags.HasErrors(), "PartialContent on %s: %s", path, diags.Error())

	out := make(map[string]bool)
	for _, b := range content.Blocks {
		if b.Type != "resource" || len(b.Labels) != 2 || b.Labels[0] != "aws_cloudwatch_metric_alarm" {
			continue
		}
		body, _, diags := b.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "metric_name"}},
		})
		require.False(t, diags.HasErrors(), "alarm body in %s: %s", path, diags.Error())
		attr, ok := body.Attributes["metric_name"]
		if !ok {
			continue
		}
		val, diags := attr.Expr.Value(nil)
		require.False(t, diags.HasErrors(),
			"evaluate metric_name in %s: %s", path, diags.Error())
		out[val.AsString()] = true
	}
	return out
}

// parseGCPAlertFilters HCL-parses the given observability.tf and
// returns the list of `filter` attribute values from every
// google_monitoring_alert_policy.conditions.condition_threshold
// nested block.
func parseGCPAlertFilters(t *testing.T, path string) []string {
	t.Helper()
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	require.False(t, diags.HasErrors(), "parse %s: %s", path, diags.Error())

	content, _, diags := file.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "resource", LabelNames: []string{"type", "name"}},
		},
	})
	require.False(t, diags.HasErrors(), "PartialContent on %s: %s", path, diags.Error())

	var filters []string
	for _, b := range content.Blocks {
		if b.Type != "resource" || len(b.Labels) != 2 || b.Labels[0] != "google_monitoring_alert_policy" {
			continue
		}
		filters = append(filters, extractAlertFilters(t, b.Body, path)...)
	}
	return filters
}

// extractAlertFilters walks conditions { condition_threshold { filter
// = ... } } nested under the alert policy body.
func extractAlertFilters(t *testing.T, body hcl.Body, path string) []string {
	t.Helper()
	policy, _, diags := body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "conditions"}},
	})
	require.False(t, diags.HasErrors(), "policy body in %s: %s", path, diags.Error())

	var out []string
	for _, cond := range policy.Blocks {
		threshold, _, diags := cond.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{Type: "condition_threshold"}},
		})
		require.False(t, diags.HasErrors(), "conditions body in %s: %s", path, diags.Error())
		for _, ct := range threshold.Blocks {
			body, _, diags := ct.Body.PartialContent(&hcl.BodySchema{
				Attributes: []hcl.AttributeSchema{{Name: "filter"}},
			})
			require.False(t, diags.HasErrors(), "condition_threshold body in %s: %s", path, diags.Error())
			attr, ok := body.Attributes["filter"]
			if !ok {
				continue
			}
			val, diags := attr.Expr.Value(nil)
			require.False(t, diags.HasErrors(),
				"evaluate filter in %s: %s", path, diags.Error())
			out = append(out, val.AsString())
		}
	}
	return out
}

// repoRoot returns the absolute path to the repository root by walking
// up from the calling test file's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	// file is /<repo>/pkg/observability/observability_alarms_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
