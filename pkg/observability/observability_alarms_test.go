package observability

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// excludedFromAuthority lists (module, metric) alarm pairs that exist in
// HCL but are intentionally NOT carried in alarmedAWSMetrics /
// alarmedGCPMetrics — typically because the alarm's metric.type has no
// corresponding entry in awsServiceMetrics / gcpServiceMetrics, or because
// the component's service catalog was deliberately scoped narrower than the
// alarm surface (e.g. gcp/bastion reuses the compute CPU metric under a
// label filter; gcp/gke alarms on kubernetes.io node metrics that the
// The InsideOut backend catalog never covered).
//
// Adding an entry requires a non-empty issue ref so the gap is tracked.
// The reverse-direction gate
// (TestObservabilityHCLAlarmsHaveCatalogOrAllowlistEntry) consults this
// list before failing on an HCL alarm with no catalog match.
//
// Key shape: "<module>:<metric_name_or_type>" (e.g.
// "gcp/gke:kubernetes.io/node/cpu/allocatable_utilization"). Module path is
// repo-relative; metric is the AWS CloudWatch metric_name attribute or the
// GCP metric.type literal (the bare metric type, not the surrounding filter
// expression).
var excludedFromAuthority = map[string]string{
	// gcp/bastion reuses the compute service CPU metric under a
	// metadata.user_labels."role"="bastion" filter — the alarm exists in
	// gcp/bastion's HCL but the catalog only carries this metric type
	// under the "compute" service (KeyGCPCompute). Tracked as a follow-up
	// so the InsideOut backend inspector grows a bastion-scoped GCPMetricSpec.
	"gcp/bastion:compute.googleapis.com/instance/cpu/utilization": "#204",
	// gcp/gke alarms on kubernetes.io/node metrics. The InsideOut backend catalog
	// has no "gke" service entry, so the alert surface is HCL-only. Add a
	// gke entry to gcpServiceMetrics in a follow-up to retire this
	// exclusion.
	"gcp/gke:kubernetes.io/node/cpu/allocatable_utilization": "#204",
}

// awsModuleAuthor inverts alarmedAWSMetrics by module path so the reverse
// gate can look up "is this metric_name in the catalog for this module?"
// in O(1) per alarm.
func awsModuleAuthor() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(alarmedAWSMetrics))
	for _, a := range alarmedAWSMetrics {
		set := out[a.Module]
		if set == nil {
			set = make(map[string]bool, len(a.Metrics))
			out[a.Module] = set
		}
		for _, m := range a.Metrics {
			set[m] = true
		}
	}
	return out
}

// gcpModuleAuthor inverts alarmedGCPMetrics. Same shape as
// awsModuleAuthor; values are metric.type literals.
func gcpModuleAuthor() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(alarmedGCPMetrics))
	for _, a := range alarmedGCPMetrics {
		set := out[a.Module]
		if set == nil {
			set = make(map[string]bool, len(a.Metrics))
			out[a.Module] = set
		}
		for _, m := range a.Metrics {
			set[m] = true
		}
	}
	return out
}

// gcpMetricTypePattern extracts the metric.type literal from a Cloud
// Monitoring filter expression. Filters look like
// `metric.type="foo.googleapis.com/bar" AND resource.type="..."`.
var gcpMetricTypePattern = regexp.MustCompile(`metric\.type="([^"]+)"`)

// TestObservabilitySpecMatchesEmittedAlarms is the C9 forward-direction
// drift gate: every (component, metric) declared in alarmedAWSMetrics
// or alarmedGCPMetrics must have a matching alarm resource in the
// referenced module's observability.tf.
//
// Reverse direction (HCL alarm with no matching catalog entry) is
// covered by TestObservabilityHCLAlarmsHaveCatalogOrAllowlistEntry, which
// fails unless the alarm is either catalog-mapped or in the explicit
// excludedFromAuthority allowlist with an issue ref.
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

// TestObservabilityHCLAlarmsHaveCatalogOrAllowlistEntry is the C9
// reverse-direction drift gate. For every aws_cloudwatch_metric_alarm and
// google_monitoring_alert_policy resource in any aws/<m>/observability.tf
// or gcp/<m>/observability.tf, the metric must be either:
//
//   - in the corresponding alarmedAWSMetrics / alarmedGCPMetrics catalog
//     entry for that module, OR
//   - in the explicit excludedFromAuthority allowlist (which itself
//     requires an issue ref via TestExcludedFromAuthority_AllHaveIssueRef).
//
// Catches the regression where an alarm is added to HCL without flipping
// the catalog Alarmed flag, leaving the inspector blind to a metric that
// production is paging on.
func TestObservabilityHCLAlarmsHaveCatalogOrAllowlistEntry(t *testing.T) {
	root := repoRoot(t)
	awsAuthor := awsModuleAuthor()
	gcpAuthor := gcpModuleAuthor()

	awsModules, err := filepath.Glob(filepath.Join(root, "aws", "*", "observability.tf"))
	require.NoError(t, err)
	for _, path := range awsModules {
		mod := relativeModule(t, root, path)
		t.Run(mod, func(t *testing.T) {
			haveByMetric := parseAWSAlarmMetricNames(t, path)
			catalog := awsAuthor[mod]
			for metric := range haveByMetric {
				if catalog[metric] {
					continue
				}
				if _, allowed := excludedFromAuthority[mod+":"+metric]; allowed {
					continue
				}
				t.Errorf("aws_cloudwatch_metric_alarm metric_name=%q in %s has no entry in alarmedAWSMetrics for module %q and is not in excludedFromAuthority — flip the catalog Alarmed flag or add an excludedFromAuthority entry with an issue ref",
					metric, path, mod)
			}
		})
	}

	gcpModules, err := filepath.Glob(filepath.Join(root, "gcp", "*", "observability.tf"))
	require.NoError(t, err)
	for _, path := range gcpModules {
		mod := relativeModule(t, root, path)
		t.Run(mod, func(t *testing.T) {
			filters := parseGCPAlertFilters(t, path)
			catalog := gcpAuthor[mod]
			for _, f := range filters {
				match := gcpMetricTypePattern.FindStringSubmatch(f)
				if len(match) != 2 {
					t.Errorf("could not extract metric.type from filter %q in %s — filter must contain a `metric.type=\"...\"` literal",
						f, path)
					continue
				}
				metric := match[1]
				if catalog[metric] {
					continue
				}
				if _, allowed := excludedFromAuthority[mod+":"+metric]; allowed {
					continue
				}
				t.Errorf("google_monitoring_alert_policy metric.type=%q in %s has no entry in alarmedGCPMetrics for module %q and is not in excludedFromAuthority — flip the catalog Alarmed flag or add an excludedFromAuthority entry with an issue ref",
					metric, path, mod)
			}
		})
	}
}

// TestExcludedFromAuthority_AllHaveIssueRef enforces the allowlist
// invariant: every excluded (module, metric) pair must be tagged with a
// non-empty issue ref so the gap remains tracked. Mirrors the
// observabilityDeferred contract.
func TestExcludedFromAuthority_AllHaveIssueRef(t *testing.T) {
	for k, ref := range excludedFromAuthority {
		assert.NotEmpty(t, strings.TrimSpace(ref),
			"excludedFromAuthority[%q] must carry a non-empty issue ref", k)
		// "<module>:<metric>" — the colon split must yield two parts.
		parts := strings.SplitN(k, ":", 2)
		assert.Len(t, parts, 2,
			"excludedFromAuthority key %q must be \"<module>:<metric>\"", k)
	}
}

// relativeModule converts an absolute observability.tf path to the
// repo-relative module path key used in alarmedAWSMetrics /
// alarmedGCPMetrics (e.g. "/abs/aws/rds/observability.tf" → "aws/rds").
func relativeModule(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, filepath.Dir(path))
	require.NoError(t, err)
	return filepath.ToSlash(rel)
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
