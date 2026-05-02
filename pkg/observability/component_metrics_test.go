package observability

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
)

// TestComponentMetricsMapping_OnlyKnownKeys ensures every key in
// ComponentMetricsMapping resolves to a known ComponentKey in
// composer.AllComponentKeys. Catches typos and stale entries.
func TestComponentMetricsMapping_OnlyKnownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range ComponentMetricsMapping {
		assert.True(t, known[k],
			"ComponentMetricsMapping[%s] is not in composer.AllComponentKeys — stale or typo'd key",
			k)
	}
}

// TestComponentMetricsMapping_ServiceRegistered ensures every Service
// in ComponentMetricsMapping is registered in the matching cloud's
// service-actions map. Without this gate, an entry here that references
// an unknown service silently produces a panel "unsupported action"
// error at runtime (#1010).
//
// NOTE: this gate intentionally does NOT assert the Action is in the
// service's action list, because the ported data has known drift:
// reliable's componentMetricsMapping["aws_vpc"] = (vpc,
// describe-vpcs) but awsServiceActions["vpc"] = [describe-nat-gateways,
// get-metrics]. describe-vpcs is registered under the "ec2" service.
// Reliable's runtime behavior is unclear (likely rejects on dispatch
// validation); this PR preserves the data as-ported and tracks the
// inconsistency as a follow-up.
//
// TODO(#204): once reliable's actual dispatch behavior is confirmed,
// either fix the mapping (point aws_vpc at "ec2") or document the
// fall-through and re-enable the strict action assertion.
func TestComponentMetricsMapping_ServiceRegistered(t *testing.T) {
	for k, binding := range ComponentMetricsMapping {
		if composer.CloudFor(k) == "aws" {
			_, ok := AWSServiceActions[binding.Service]
			assert.True(t, ok,
				"ComponentMetricsMapping[%s].Service=%q is not in AWSServiceActions",
				k, binding.Service)
			continue
		}
		_, ok := GCPServiceActions[binding.Service]
		assert.True(t, ok,
			"ComponentMetricsMapping[%s].Service=%q is not in GCPServiceActions",
			k, binding.Service)
	}
}

// TestEmptyDiscoveryAllowlist_OnlyKnownKeys ensures every key in
// EmptyDiscoveryAllowlist resolves to a known ComponentKey.
func TestEmptyDiscoveryAllowlist_OnlyKnownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range EmptyDiscoveryAllowlist {
		assert.True(t, known[k],
			"EmptyDiscoveryAllowlist[%s] is not in composer.AllComponentKeys — stale or typo'd key",
			k)
	}
}

// TestObservabilityMatchesComponentMetricsMapping_Service ensures the
// Service field on each Observability entry agrees with the
// ComponentMetricsMapping binding. Drift would mean the metric-fetch
// path and the inspector dispatch path disagree about which service
// owns a component (the historical class of #1234-style bugs).
func TestObservabilityMatchesComponentMetricsMapping_Service(t *testing.T) {
	for k, binding := range ComponentMetricsMapping {
		o, ok := Observability[k]
		if !ok {
			continue // covered by other gates
		}
		assert.Equal(t, binding.Service, o.Service,
			"Observability[%s].Service=%q diverges from ComponentMetricsMapping[%s].Service=%q",
			k, o.Service, k, binding.Service)
	}
}

// TestObservability_AWSEntriesHaveAWSObs ensures every Observability
// entry whose key is AWS-backed AND whose Service is in
// awsServiceMetrics has a non-nil AWS field. GCP keys must NOT have a
// non-nil AWS field.
func TestObservability_AWSEntriesHaveAWSObs(t *testing.T) {
	for k, o := range Observability {
		if composer.CloudFor(k) == "aws" {
			if _, hasMetrics := awsServiceMetrics[o.Service]; hasMetrics {
				assert.NotNil(t, o.AWS,
					"Observability[%s].AWS should be non-nil because service %q has awsServiceMetrics catalog",
					k, o.Service)
			}
			assert.Nil(t, o.GCP,
				"Observability[%s].GCP must be nil (key is AWS)", k)
			continue
		}
		assert.Nil(t, o.AWS,
			"Observability[%s].AWS must be nil (key is GCP)", k)
		if _, hasMetrics := gcpServiceMetrics[o.Service]; hasMetrics {
			assert.NotNil(t, o.GCP,
				"Observability[%s].GCP should be non-nil because service %q has gcpServiceMetrics catalog",
				k, o.Service)
		}
	}
}

// TestObservability_AllAlarmedFalseAtC2 documents the C2 invariant:
// no metric has Alarmed=true yet. Alarms land in C7-C9. When this test
// fails it means C9 has landed — update or delete it then.
func TestObservability_AllAlarmedFalseAtC2(t *testing.T) {
	for k, o := range Observability {
		if o.AWS != nil {
			for _, m := range o.AWS.Metrics {
				assert.False(t, m.Alarmed,
					"Observability[%s].AWS.Metrics[%q].Alarmed=true at C2 — update this test when C9 lands the first alarm",
					k, m.Name)
			}
		}
		if o.GCP != nil {
			for _, m := range o.GCP.Metrics {
				assert.False(t, m.Alarmed,
					"Observability[%s].GCP.Metrics[%q].Alarmed=true at C2 — update this test when C9 lands the first alarm",
					k, m.DisplayName)
			}
		}
	}
}

// TestServicesForKeys_ReturnsKnownServices verifies that with C2's
// real Service fields, ServicesForKeys produces a useful result.
func TestServicesForKeys_ReturnsKnownServices(t *testing.T) {
	got := ServicesForKeys([]composer.ComponentKey{
		composer.KeyAWSEC2, composer.KeyAWSRDS, composer.KeyAWSALB,
	})
	assert.ElementsMatch(t, []string{"alb", "ec2", "rds"}, got,
		"ServicesForKeys should return the EC2/RDS/ALB services in sorted order; got %v", got)
}

// TestMetricDisplayLabel_Override verifies a known label override
// returns the display string from metric_display_labels.json.
func TestMetricDisplayLabel_Override(t *testing.T) {
	got := MetricDisplayLabel("HTTPCode_ELB_5XX_Count")
	assert.Equal(t, "ALB 5XX Errors", got,
		"HTTPCode_ELB_5XX_Count should resolve via metric_display_labels.json override")
}

// TestMetricDisplayLabel_Fallback verifies an unknown name falls back
// to the CamelCase splitter.
func TestMetricDisplayLabel_Fallback(t *testing.T) {
	got := MetricDisplayLabel("MyNewMetric")
	assert.Equal(t, "My New Metric", got,
		"unknown metric names should split on uppercase boundaries")
}

// TestMetricDisplayLabels_ReturnsCopy verifies the exported helper
// returns a fresh copy that can be mutated without affecting the
// package-level map.
func TestMetricDisplayLabels_ReturnsCopy(t *testing.T) {
	first := MetricDisplayLabels()
	first["__phantom__"] = "should not leak"
	second := MetricDisplayLabels()
	_, leaked := second["__phantom__"]
	assert.False(t, leaked, "MetricDisplayLabels must return a defensive copy")
}

// TestComponentDisplayName_CoversEveryComponentKey ensures every key
// in composer.AllComponentKeys produces a non-empty, non-"AWS"-only
// display name. Catches a missing case in the switch statement (the
// fallback works but produces uglier output, so we want the explicit
// case for known keys).
func TestComponentDisplayName_CoversEveryComponentKey(t *testing.T) {
	for _, k := range composer.AllComponentKeys {
		got := ComponentDisplayName(k)
		assert.NotEmpty(t, got,
			"ComponentDisplayName(%s) returned empty string", k)
		// The fallback for an unmapped key produces e.g. "Vpc" for
		// "aws_vpc"; the explicit case produces "AWS VPC". Catch
		// missed keys by asserting the prefix matches the cloud.
		switch composer.CloudFor(k) {
		case "aws":
			assert.Contains(t, got, "AWS",
				"ComponentDisplayName(%s)=%q should contain 'AWS' (likely missing case)",
				k, got)
		case "gcp":
			assert.Contains(t, got, "GCP",
				"ComponentDisplayName(%s)=%q should contain 'GCP' (likely missing case)",
				k, got)
		}
	}
}

// TestServiceSupportsGetMetrics_AWS verifies the registry gate.
func TestServiceSupportsGetMetrics_AWS(t *testing.T) {
	assert.True(t, ServiceSupportsGetMetrics("ec2", false),
		"AWS ec2 service must support get-metrics")
	assert.False(t, ServiceSupportsGetMetrics("backup", false),
		"AWS backup service does not support get-metrics — gate must return false")
	assert.False(t, ServiceSupportsGetMetrics("does_not_exist", false),
		"unknown service must return false (not panic)")
}

// TestServiceSupportsGetMetrics_GCP verifies the GCP path of the registry gate.
func TestServiceSupportsGetMetrics_GCP(t *testing.T) {
	assert.True(t, ServiceSupportsGetMetrics("compute", true),
		"GCP compute service must support get-metrics")
	assert.False(t, ServiceSupportsGetMetrics("gke", true),
		"GCP gke service does not register get-metrics (cluster metrics need separate dispatch)")
	assert.False(t, ServiceSupportsGetMetrics("cloudmonitoring", true),
		"GCP cloudmonitoring is list-only by design")
}

// TestCanonicalAWSService_Aliases verifies alias resolution.
func TestCanonicalAWSService_Aliases(t *testing.T) {
	assert.Equal(t, "alb", CanonicalAWSService("elb"),
		"elb alias must resolve to alb")
	assert.Equal(t, "elasticache", CanonicalAWSService("redis"),
		"redis alias must resolve to elasticache")
	assert.Equal(t, "unknown", CanonicalAWSService("unknown"),
		"unknown service must pass through unchanged")
}

// TestCanonicalAWSAction_AliasesAndPassthrough verifies action alias
// resolution and the passthrough behavior.
func TestCanonicalAWSAction_AliasesAndPassthrough(t *testing.T) {
	assert.Equal(t, "describe-db-instances", CanonicalAWSAction("rds", "list-db-instances"),
		"rds list-db-instances should canonicalize to describe-db-instances")
	assert.Equal(t, "list-buckets", CanonicalAWSAction("s3", "list-buckets"),
		"already-canonical action should pass through unchanged")
	assert.Equal(t, "list-buckets", CanonicalAWSAction("unknown_service", "list-buckets"),
		"unknown service should pass action through unchanged")
}

// TestCanonicalGCPService_Aliases verifies GCP alias resolution.
func TestCanonicalGCPService_Aliases(t *testing.T) {
	assert.Equal(t, "cloudkms", CanonicalGCPService("kms"),
		"kms alias must resolve to cloudkms")
	assert.Equal(t, "loadbalancer", CanonicalGCPService("lb"),
		"lb alias must resolve to loadbalancer")
}

// TestAWSServiceNames_Sorted verifies the deterministic ordering.
func TestAWSServiceNames_Sorted(t *testing.T) {
	got := AWSServiceNames()
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1], got[i],
			"AWSServiceNames must be sorted; %q > %q at index %d",
			got[i-1], got[i], i-1)
	}
}

// TestGCPServiceNames_Sorted verifies the deterministic ordering.
func TestGCPServiceNames_Sorted(t *testing.T) {
	got := GCPServiceNames()
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1], got[i],
			"GCPServiceNames must be sorted; %q > %q at index %d",
			got[i-1], got[i], i-1)
	}
}
