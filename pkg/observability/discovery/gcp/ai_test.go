package gcp

import (
	"encoding/json"
	"testing"

	"cloud.google.com/go/aiplatform/apiv1/aiplatformpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Empty-state pins for the three Vertex AI list-* sites (#256). The
// inspector wraps drainIterator with an emit-region-hint side-effect
// that fires only when len==0 AND no region was specified — the hint
// doesn't change the JSON shape, so the contract is the same as any
// other drainIterator caller.

func TestInspectVertexAI_ListDatasets_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*aiplatformpb.Dataset]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Vertex AI list-datasets must marshal as [] not null (#256)")
}

func TestInspectVertexAI_ListEndpoints_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*aiplatformpb.Endpoint]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Vertex AI list-endpoints must marshal as [] not null (#256)")
}

func TestInspectVertexAI_ListModels_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*aiplatformpb.Model]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Vertex AI list-models must marshal as [] not null (#256)")
}

// TestExtractVertexAIRegion locks the JSON key + default region for
// the Vertex AI inspector. Mirrors the InsideOut backend's
// TestVertexAIRegionExtraction (gcp_inspect.go references; the test in
// The InsideOut backend validates the same shape). If this regresses, callers
// asking for "describe my Vertex deployment" without a region filter
// silently query the wrong region — that's the bug we'd ship.
func TestExtractVertexAIRegion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		filters     string
		wantRegion  string
		wantExplict bool
	}{
		{"empty -> default", "", vertexAIDefaultRegion, false},
		{"no region key -> default", `{"project":"io-foo"}`, vertexAIDefaultRegion, false},
		{"explicit region", `{"region":"europe-west1"}`, "europe-west1", true},
		{"empty region value falls back to default", `{"region":""}`, vertexAIDefaultRegion, false},
		{"malformed JSON -> default", "garbage", vertexAIDefaultRegion, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, gotExplicit := extractVertexAIRegion(tc.filters)
			assert.Equal(t, tc.wantRegion, got)
			assert.Equal(t, tc.wantExplict, gotExplicit)
		})
	}
}

// TestVertexAIDefaultRegionConstant is a static reminder that the
// default region is a behavioural contract — changing it shifts every
// caller's "what region are we querying" answer when no filter is
// passed.
func TestVertexAIDefaultRegionConstant(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "us-central1", vertexAIDefaultRegion,
		"changing the default region breaks every caller relying on the implicit default")
}
