package observability

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGCPMetricDefinitions_FirestoreResourceType pins the firestore
// service-catalog entry against regression on TWO fronts:
//
//  1. Every metric must use the canonical
//     "firestore.googleapis.com/Database" resource type (the only
//     resource type that carries database_id, location, and
//     resource_container labels). The legacy "firestore_instance"
//     resource only carries project_id, so panels using it cannot
//     scope per-database.
//  2. Every metric must publish under that resource type — verified
//     against Cloud Monitoring's MetricDescriptors API. Picking a
//     metric whose monitoredResourceTypes doesn't include
//     "firestore.googleapis.com/Database" silently produces empty
//     queries (this trapped reliable#1259's first attempt: the legacy
//     document/{read,write,delete}_count metrics only publish under
//     firestore_instance — their *_ops_count modern variants are the
//     ones that publish under Database).
//
// Reliable historically used "firestore_instance"; reliable#1259
// fixed that for request_latencies but accidentally shipped the
// non-publishing legacy *_count names on Database for the other
// three. This pin trips on either mistake.
func TestGCPMetricDefinitions_FirestoreResourceType(t *testing.T) {
	t.Parallel()

	def, ok := gcpServiceMetrics["firestore"]
	require.True(t, ok, `gcpServiceMetrics["firestore"] must be present`)
	require.Len(t, def.Metrics, 4,
		"firestore catalog must declare exactly four metrics (request_latencies + document {read,write,delete}_ops_count)")

	const wantResourceType = "firestore.googleapis.com/Database"
	for i, m := range def.Metrics {
		assert.Equal(t, wantResourceType, m.ResourceType,
			"firestore metric[%d] (%s) must use canonical ResourceType %q (not the legacy %q)",
			i, m.MetricType, wantResourceType, "firestore_instance")
	}

	// Pin metric-type set + order. Order matters because
	// alarmedGCPMetrics flips Alarmed by name match and the panel
	// renders metrics in slice order; reordering changes the user-
	// visible default chart layout. The *_ops_count names are the
	// modern variants that actually publish under
	// firestore.googleapis.com/Database (the legacy *_count names only
	// publish under the firestore_instance resource type and would
	// return zero data here).
	wantOrder := []string{
		"firestore.googleapis.com/api/request_latencies",
		"firestore.googleapis.com/document/read_ops_count",
		"firestore.googleapis.com/document/write_ops_count",
		"firestore.googleapis.com/document/delete_ops_count",
	}
	gotOrder := make([]string, 0, len(def.Metrics))
	for _, m := range def.Metrics {
		gotOrder = append(gotOrder, m.MetricType)
	}
	assert.Equal(t, wantOrder, gotOrder,
		"firestore metric names have drifted; the *_ops_count variants publish under firestore.googleapis.com/Database while the legacy *_count variants only publish under firestore_instance")
}

// TestGCPMetricDefinitions_FirestoreAlarmBinding pins the
// alarm-author handshake half of reliable#1259's fix: the alarm bound
// to KeyGCPFirestore must reference a metric_type that exists in the
// catalog above, otherwise componentObs() can't flip Alarmed=true and
// TestObservabilitySpecMatchesEmittedAlarms (the HCL-side gate) loses
// its upstream pair.
func TestGCPMetricDefinitions_FirestoreAlarmBinding(t *testing.T) {
	t.Parallel()

	author, ok := alarmedGCPMetrics[composer.KeyGCPFirestore]
	require.True(t, ok, "alarmedGCPMetrics must bind KeyGCPFirestore")
	require.NotEmpty(t, author.Metrics, "KeyGCPFirestore alarm author must declare at least one metric_type")

	// Every alarmed metric_type must exist in the catalog so
	// componentObs() can flip Alarmed.
	catalog := make(map[string]struct{}, len(gcpServiceMetrics["firestore"].Metrics))
	for _, m := range gcpServiceMetrics["firestore"].Metrics {
		catalog[m.MetricType] = struct{}{}
	}
	for _, mt := range author.Metrics {
		_, present := catalog[mt]
		assert.Truef(t, present,
			"alarmedGCPMetrics[KeyGCPFirestore] references %q which is not in gcpServiceMetrics[\"firestore\"]; the Alarmed flag will never be set",
			mt)
	}
}
