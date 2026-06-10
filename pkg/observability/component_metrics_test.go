package observability

import (
	"slices"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// componentMetricsActionAllowlist records ComponentMetricsMapping
// entries whose Action is intentionally NOT in the registered
// AWSServiceActions/GCPServiceActions list for the bound Service.
// Each entry needs a justification — the gate exists precisely to make
// this kind of drift loud (Vertex AI's list-endpoints typo in #253 is
// what motivated it).
var componentMetricsActionAllowlist = map[composer.ComponentKey]string{
	// aws_vpc binds (vpc, describe-vpcs) but describe-vpcs is registered
	// under the "ec2" service per the AWS SDK split. Either fix the
	// mapping (point aws_vpc at "ec2") or move describe-vpcs into the
	// "vpc" service's action list — both touch downstream dispatchers.
	// Tracked separately as a follow-up to #253.
	composer.KeyAWSVPC: "describe-vpcs is registered under ec2, not vpc — needs cross-repo dispatch alignment",
}

// TestComponentMetricsMapping_ActionRegistered is the forward-direction
// gate that would have caught Vertex AI's list-endpoints typo in #253.
// For every entry in ComponentMetricsMapping, assert the Action is in
// the registered action list of the bound Service. Mismatches are
// allowlisted via componentMetricsActionAllowlist with a justification.
func TestComponentMetricsMapping_ActionRegistered(t *testing.T) {
	for k, binding := range ComponentMetricsMapping {
		if reason, exempt := componentMetricsActionAllowlist[k]; exempt {
			t.Logf("allowlisted: %s (%s)", k, reason)
			continue
		}
		registry := AWSServiceActions
		if composer.CloudFor(k) == "gcp" {
			registry = GCPServiceActions
		}
		actions, ok := registry[binding.Service]
		if !ok {
			// Already covered by TestComponentMetricsMapping_ServiceRegistered.
			continue
		}
		assert.True(t, slices.Contains(actions, binding.Action),
			"ComponentMetricsMapping[%s].Action=%q is not in the registered action list for service %q (have %v) — adding the action to the service registry, fixing the mapping, or allowlisting in componentMetricsActionAllowlist with a justification will all unblock",
			k, binding.Action, binding.Service, actions)
	}
}

// TestComponentMetricsActionAllowlist_NotStale guards against the
// allowlist outliving its purpose — every entry must point at a real
// ComponentMetricsMapping binding.
func TestComponentMetricsActionAllowlist_NotStale(t *testing.T) {
	for k := range componentMetricsActionAllowlist {
		_, ok := ComponentMetricsMapping[k]
		assert.True(t, ok,
			"componentMetricsActionAllowlist entry %q has no matching ComponentMetricsMapping binding — drop it",
			k)
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
// non-nil AWS field. Any AWSExtra groups present (multi-namespace
// components, #778) must be non-nil and only attached to AWS keys.
func TestObservability_AWSEntriesHaveAWSObs(t *testing.T) {
	for k, o := range Observability {
		if composer.CloudFor(k) == "aws" {
			if _, hasMetrics := awsServiceMetrics[o.Service]; hasMetrics {
				assert.NotNil(t, o.AWS,
					"Observability[%s].AWS should be non-nil because service %q has awsServiceMetrics catalog",
					k, o.Service)
			}
			for i, g := range o.AWSExtra {
				assert.NotNil(t, g,
					"Observability[%s].AWSExtra[%d] must be non-nil — componentObs only appends real groups", k, i)
			}
			assert.Nil(t, o.GCP,
				"Observability[%s].GCP must be nil (key is AWS)", k)
			continue
		}
		assert.Nil(t, o.AWS,
			"Observability[%s].AWS must be nil (key is GCP)", k)
		assert.Empty(t, o.AWSExtra,
			"Observability[%s].AWSExtra must be empty (key is GCP)", k)
		if _, hasMetrics := gcpServiceMetrics[o.Service]; hasMetrics {
			assert.NotNil(t, o.GCP,
				"Observability[%s].GCP should be non-nil because service %q has gcpServiceMetrics catalog",
				k, o.Service)
		}
	}
}

// TestObservability_OpenSearchHasAOSSGroup is the #778 multi-group
// contract pinned to the data model: aws_opensearch carries the managed
// AWS/ES + DomainName group as its primary AWS group AND the serverless
// AWS/AOSS + ClientId group in AWSExtra. SearchOCU / IndexingOCU live in
// the AOSS group with Alarmed=true (flipped from
// alarmedAWSMetrics[KeyAWSOpenSearch]) and the Sum stat.
func TestObservability_OpenSearchHasAOSSGroup(t *testing.T) {
	o, ok := Observability[composer.KeyAWSOpenSearch]
	require.True(t, ok, "aws_opensearch must have an Observability record")

	require.NotNil(t, o.AWS, "primary AWS group (managed AWS/ES) must be present")
	assert.Equal(t, "AWS/ES", o.AWS.Namespace)
	assert.Equal(t, "DomainName", o.AWS.DimensionName)

	require.Len(t, o.AWSExtra, 1, "aws_opensearch must carry exactly one extra group (AOSS)")
	aoss := o.AWSExtra[0]
	require.NotNil(t, aoss)
	assert.Equal(t, "AWS/AOSS", aoss.Namespace)
	assert.Equal(t, "ClientId", aoss.DimensionName)

	byName := make(map[string]AWSMetricSpec, len(aoss.Metrics))
	for _, m := range aoss.Metrics {
		byName[m.Name] = m
	}
	for _, name := range []string{"SearchOCU", "IndexingOCU"} {
		spec, present := byName[name]
		require.True(t, present, "AOSS group must carry %q", name)
		assert.Equal(t, "Sum", spec.Stat, "%q must use the Sum stat", name)
		assert.True(t, spec.Alarmed,
			"%q must be Alarmed=true — it is registered in alarmedAWSMetrics[KeyAWSOpenSearch]", name)
	}

	// AWSGroups() exposes both groups in order for the metric-fetch path.
	groups := o.AWSGroups()
	require.Len(t, groups, 2)
	assert.Equal(t, "AWS/ES", groups[0].Namespace)
	assert.Equal(t, "AWS/AOSS", groups[1].Namespace)
}

// TestObservability_AlarmedSpecsHaveAuthorityEntry is the post-C9
// invariant complementing TestObservabilitySpecMatchesEmittedAlarms:
// every Alarmed=true spec must originate from alarmedAWSMetrics /
// alarmedGCPMetrics. Catches accidental Alarmed=true left in the
// service catalog (which is intentionally Alarmed=false everywhere
// because catalogs are shared across multiple component keys).
func TestObservability_AlarmedSpecsHaveAuthorityEntry(t *testing.T) {
	for k, o := range Observability {
		var awsAuthority, gcpAuthority map[string]bool
		if a, ok := alarmedAWSMetrics[k]; ok {
			awsAuthority = make(map[string]bool, len(a.Metrics))
			for _, m := range a.Metrics {
				awsAuthority[m] = true
			}
		}
		if a, ok := alarmedGCPMetrics[k]; ok {
			gcpAuthority = make(map[string]bool, len(a.Metrics))
			for _, m := range a.Metrics {
				gcpAuthority[m] = true
			}
		}
		// Walk every AWS group (primary + AWSExtra) so an Alarmed flip in a
		// multi-namespace component's extra group (the AOSS SearchOCU /
		// IndexingOCU, #778) is held to the same authority contract.
		for _, g := range o.AWSGroups() {
			for _, m := range g.Metrics {
				if !m.Alarmed {
					continue
				}
				assert.True(t, awsAuthority[m.Name],
					"Observability[%s].AWS group (namespace %s) metric %q has Alarmed=true but %q not in alarmedAWSMetrics[%s] — service catalog must remain Alarmed=false; flips happen via the per-key authority map",
					k, g.Namespace, m.Name, m.Name, k)
			}
		}
		if o.GCP != nil {
			for _, m := range o.GCP.Metrics {
				if !m.Alarmed {
					continue
				}
				assert.True(t, gcpAuthority[m.MetricType],
					"Observability[%s].GCP.Metrics[%q].Alarmed=true but %q not in alarmedGCPMetrics[%s]",
					k, m.MetricType, m.MetricType, k)
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
