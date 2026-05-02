package observability

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGCPMetricDefinitions_FirestoreResourceType pins the firestore
// service-catalog entry against regression. Cloud Monitoring publishes
// firestore time series under resource.type =
// "firestore.googleapis.com/Database"; reliable historically used the
// invalid "firestore_instance" string, which silently produced empty
// queries. The four metric types here are the surface that
// alarmedGCPMetrics[KeyGCPFirestore] expects to flip Alarmed=true on
// (request_latencies) — losing any of them dim-sums the firestore
// panel and the alarm-author handshake.
//
// Ported from reliable PR #1259, which fixed the same bug in
// reliable's local catalog. The fix has lived upstream here since
// v0.7.x; this test is the regression pin so future edits to
// gcpServiceMetrics["firestore"] can't silently re-introduce the
// "firestore_instance" mistake.
func TestGCPMetricDefinitions_FirestoreResourceType(t *testing.T) {
	t.Parallel()

	def, ok := gcpServiceMetrics["firestore"]
	require.True(t, ok, `gcpServiceMetrics["firestore"] must be present`)
	require.Len(t, def.Metrics, 4,
		"firestore catalog must declare exactly four metrics (request_latencies + document {read,write,delete}_count)")

	const wantResourceType = "firestore.googleapis.com/Database"
	for i, m := range def.Metrics {
		assert.Equal(t, wantResourceType, m.ResourceType,
			"firestore metric[%d] (%s) must use canonical ResourceType %q (not the legacy %q)",
			i, m.MetricType, wantResourceType, "firestore_instance")
	}

	// Pin metric-type set + order. Order matters because
	// alarmedGCPMetrics flips Alarmed by name match and the panel
	// renders metrics in slice order; reordering changes the user-
	// visible default chart layout.
	wantOrder := []string{
		"firestore.googleapis.com/api/request_latencies",
		"firestore.googleapis.com/document/read_count",
		"firestore.googleapis.com/document/write_count",
		"firestore.googleapis.com/document/delete_count",
	}
	gotOrder := make([]string, 0, len(def.Metrics))
	for _, m := range def.Metrics {
		gotOrder = append(gotOrder, m.MetricType)
	}
	assert.Equal(t, wantOrder, gotOrder,
		"firestore metric order has drifted; reliable#1259 expected request_latencies first so the alarm-author handshake (alarmedGCPMetrics[KeyGCPFirestore]) lands on Alarmed=true")
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
