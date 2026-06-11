package composer

// vertex_ai_gcp_wiring_test.go covers the issue #764 composer surface for the
// gcp/vertex_ai Vector Search deepening:
//
//   - The KeyGCPVertexAI mapper case is partial-config: it only emits a tfvar
//     the caller actually populated, so the preset's own defaults win when a
//     field is left unset (mirrors the AWS Bedrock pattern from #757).
//   - DefaultWiring feeds the index endpoint's network from gcp/vpc (the
//     preset converts it to the project-NUMBER form the API needs and the
//     endpoint stays public unless enable_private_endpoint is set) and the
//     index's contents_delta_uri from a dedicated gcp/gcs bucket prefix when
//     those components are selected, and stays inert for both when they are
//     not.
//
// The registry plumbing (ComponentKey + PresetKeyMap + ModulePath +
// AllComponentKeys + ComposeOrder) and the required-variable coverage are
// exercised by the sibling invariant gates (TestMapperKeysSubsetOfModuleVariables,
// TestEveryRequiredVariableIsMappedOrWired).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildModuleValues_GCPVertexAI_PartialConfig(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}
	tr := true
	fa := false

	t.Run("nil GCPVertexAI emits nothing (preset defaults win)", func(t *testing.T) {
		t.Parallel()
		vals, err := m.BuildModuleValues(KeyGCPVertexAI, nil, &Config{}, "demo", "us-central1")
		require.NoError(t, err)
		_, hasEnable := vals["enable_vector_search"]
		assert.False(t, hasEnable, "nil GCPVertexAI must not emit enable_vector_search")
		_, hasDims := vals["index_dimensions"]
		assert.False(t, hasDims, "nil GCPVertexAI must not emit index_dimensions")
		_, hasServing := vals["enable_serving"]
		assert.False(t, hasServing, "nil GCPVertexAI must not emit enable_serving")
		_, hasModel := vals["model_garden_model"]
		assert.False(t, hasModel, "nil GCPVertexAI must not emit model_garden_model")
	})

	t.Run("EnableVectorSearch=true flows through", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		cfg.GCPVertexAI = &struct {
			EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
			IndexDimensions       int    `json:"indexDimensions,omitempty"`
			EnableServing         *bool  `json:"enableServing,omitempty"`
			ModelGardenModel      string `json:"modelGardenModel,omitempty"`
			ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
		}{EnableVectorSearch: &tr, IndexDimensions: 1536}
		vals, err := m.BuildModuleValues(KeyGCPVertexAI, nil, cfg, "demo", "us-central1")
		require.NoError(t, err)
		assert.Equal(t, true, vals["enable_vector_search"])
		assert.Equal(t, 1536, vals["index_dimensions"])
		// Serving fields untouched here -> not emitted (preset defaults win).
		_, hasServing := vals["enable_serving"]
		assert.False(t, hasServing, "unset EnableServing must not emit enable_serving")
		_, hasModel := vals["model_garden_model"]
		assert.False(t, hasModel, "unset ModelGardenModel must not emit model_garden_model")
	})

	t.Run("EnableVectorSearch=false flows through, zero dimensions omitted", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		cfg.GCPVertexAI = &struct {
			EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
			IndexDimensions       int    `json:"indexDimensions,omitempty"`
			EnableServing         *bool  `json:"enableServing,omitempty"`
			ModelGardenModel      string `json:"modelGardenModel,omitempty"`
			ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
		}{EnableVectorSearch: &fa}
		vals, err := m.BuildModuleValues(KeyGCPVertexAI, nil, cfg, "demo", "us-central1")
		require.NoError(t, err)
		assert.Equal(t, false, vals["enable_vector_search"])
		// IndexDimensions left at its zero value must NOT be emitted, so the
		// preset's own default (768) applies rather than an invalid 0.
		_, hasDims := vals["index_dimensions"]
		assert.False(t, hasDims, "zero IndexDimensions must be omitted so the preset default wins")
	})

	t.Run("EnableServing + ModelGardenModel flow through (#768)", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		cfg.GCPVertexAI = &struct {
			EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
			IndexDimensions       int    `json:"indexDimensions,omitempty"`
			EnableServing         *bool  `json:"enableServing,omitempty"`
			ModelGardenModel      string `json:"modelGardenModel,omitempty"`
			ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
		}{EnableServing: &tr, ModelGardenModel: "publishers/google/models/gemma3@gemma-3-1b-it"}
		vals, err := m.BuildModuleValues(KeyGCPVertexAI, nil, cfg, "demo", "us-central1")
		require.NoError(t, err)
		assert.Equal(t, true, vals["enable_serving"])
		assert.Equal(t, "publishers/google/models/gemma3@gemma-3-1b-it", vals["model_garden_model"])
		// Vector Search fields untouched -> not emitted (orthogonal flags).
		_, hasVS := vals["enable_vector_search"]
		assert.False(t, hasVS, "unset EnableVectorSearch must not emit enable_vector_search when only serving is set")
	})

	t.Run("ModelGardenAcceptEULA flows through only when set (#768 review)", func(t *testing.T) {
		t.Parallel()

		// Set true -> emitted (EULA-gated Gemma/Llama need it).
		cfgYes := &Config{}
		cfgYes.GCPVertexAI = &struct {
			EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
			IndexDimensions       int    `json:"indexDimensions,omitempty"`
			EnableServing         *bool  `json:"enableServing,omitempty"`
			ModelGardenModel      string `json:"modelGardenModel,omitempty"`
			ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
		}{EnableServing: &tr, ModelGardenModel: "publishers/google/models/gemma3@gemma-3-1b-it", ModelGardenAcceptEULA: &tr}
		vals, err := m.BuildModuleValues(KeyGCPVertexAI, nil, cfgYes, "demo", "us-central1")
		require.NoError(t, err)
		assert.Equal(t, true, vals["model_garden_accept_eula"], "ModelGardenAcceptEULA=true must reach model_garden_accept_eula")

		// Set false -> still emitted (explicit non-consent is a real choice the
		// caller made; partial-config keys on nil, not on the zero value).
		cfgNo := &Config{}
		cfgNo.GCPVertexAI = &struct {
			EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
			IndexDimensions       int    `json:"indexDimensions,omitempty"`
			EnableServing         *bool  `json:"enableServing,omitempty"`
			ModelGardenModel      string `json:"modelGardenModel,omitempty"`
			ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
		}{EnableServing: &tr, ModelGardenModel: "publishers/google/models/gemma3@gemma-3-1b-it", ModelGardenAcceptEULA: &fa}
		vals, err = m.BuildModuleValues(KeyGCPVertexAI, nil, cfgNo, "demo", "us-central1")
		require.NoError(t, err)
		assert.Equal(t, false, vals["model_garden_accept_eula"], "explicit ModelGardenAcceptEULA=false must reach model_garden_accept_eula")

		// Unset (nil) -> NOT emitted so the preset's explicit-consent default
		// (false) wins rather than a config-supplied value.
		cfgUnset := &Config{}
		cfgUnset.GCPVertexAI = &struct {
			EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
			IndexDimensions       int    `json:"indexDimensions,omitempty"`
			EnableServing         *bool  `json:"enableServing,omitempty"`
			ModelGardenModel      string `json:"modelGardenModel,omitempty"`
			ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
		}{EnableServing: &tr, ModelGardenModel: "publishers/google/models/gemma3@gemma-3-1b-it"}
		vals, err = m.BuildModuleValues(KeyGCPVertexAI, nil, cfgUnset, "demo", "us-central1")
		require.NoError(t, err)
		_, hasEULA := vals["model_garden_accept_eula"]
		assert.False(t, hasEULA, "unset ModelGardenAcceptEULA must not emit model_garden_accept_eula (preset default wins)")
	})

	t.Run("EnableServing=false flows through, empty model omitted (#768)", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		cfg.GCPVertexAI = &struct {
			EnableVectorSearch    *bool  `json:"enableVectorSearch,omitempty"`
			IndexDimensions       int    `json:"indexDimensions,omitempty"`
			EnableServing         *bool  `json:"enableServing,omitempty"`
			ModelGardenModel      string `json:"modelGardenModel,omitempty"`
			ModelGardenAcceptEULA *bool  `json:"modelGardenAcceptEula,omitempty"`
		}{EnableServing: &fa}
		vals, err := m.BuildModuleValues(KeyGCPVertexAI, nil, cfg, "demo", "us-central1")
		require.NoError(t, err)
		assert.Equal(t, false, vals["enable_serving"])
		// Empty ModelGardenModel must NOT be emitted so the preset default
		// (null -> bare endpoint) wins rather than an invalid empty string.
		_, hasModel := vals["model_garden_model"]
		assert.False(t, hasModel, "empty ModelGardenModel must be omitted so the preset default wins")
	})
}

func TestDefaultWiring_GCPVertexAI_NetworkAndGCS(t *testing.T) {
	t.Parallel()

	// Full stack: VPC + GCS selected -> the index endpoint network is wired
	// from gcp/vpc (the preset reshapes it to the project-NUMBER form and keeps
	// the endpoint public unless enable_private_endpoint is set), and the index
	// is seeded from a dedicated prefix under the bucket.
	selected := map[ComponentKey]bool{
		KeyGCPVertexAI: true,
		KeyGCPVPC:      true,
		KeyGCPGCS:      true,
	}
	wi := DefaultWiring(selected, KeyGCPVertexAI, &Components{})

	require.Contains(t, wi.RawHCL, "network",
		"VPC selected -> the index endpoint network input must be wired from the VPC")
	assert.Equal(t, WireRef(KeyGCPVPC, "vpc_id"), wi.RawHCL["network"],
		"network must reference gcp/vpc.vpc_id; the preset converts the project-ID path to the project-NUMBER path the API requires")

	require.Contains(t, wi.RawHCL, "contents_delta_uri",
		"GCS selected -> the index must be seeded from the bucket")
	// Wired to a dedicated prefix under the bucket, NOT the bucket root —
	// Vertex's contents_delta_uri expects a directory of index data files.
	assert.Equal(t, "\"${"+WireRef(KeyGCPGCS, "bucket_url")+"}/vertex-index/\"", wi.RawHCL["contents_delta_uri"],
		"contents_delta_uri must reference a gcp/gcs.bucket_url subdirectory (gs://<bucket>/vertex-index/), not the bucket root")

	assert.Contains(t, wi.Names, "network")
	assert.Contains(t, wi.Names, "contents_delta_uri")
}

func TestDefaultWiring_GCPVertexAI_InertStandalone(t *testing.T) {
	t.Parallel()

	// Standalone preview: neither VPC nor GCS selected -> no wiring, so the
	// preset's public-endpoint + empty-index defaults apply.
	selected := map[ComponentKey]bool{
		KeyGCPVertexAI: true,
	}
	wi := DefaultWiring(selected, KeyGCPVertexAI, &Components{})

	assert.NotContains(t, wi.RawHCL, "network",
		"no VPC selected -> no network wiring (endpoint is public)")
	assert.NotContains(t, wi.RawHCL, "contents_delta_uri",
		"no GCS selected -> the index must be created empty (no contents_delta_uri wiring)")
}
