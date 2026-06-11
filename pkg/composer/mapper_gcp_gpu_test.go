package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildModuleValues_GCPCompute_GPU exercises the gcp_compute GPU mapper
// branch (#767): partial-config defaulting and rejection-with-content. The
// preset forces on_host_maintenance=TERMINATE by construction, so the mapper
// only emits gpu_type / gpu_count.
func TestBuildModuleValues_GCPCompute_GPU(t *testing.T) {
	m := DefaultMapper{}

	t.Run("explicit GPUType on N1 emits gpu_type + default count", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "n1-standard-4", GPUType: "nvidia-tesla-t4"})
		vals, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-tesla-t4", vals["gpu_type"])
		assert.Equal(t, 1, vals["gpu_count"], "GPUType with no count must default to 1")
	})

	t.Run("GPUCount only on N1 defaults the accelerator type", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "n1-standard-8", GPUCount: 2})
		vals, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-tesla-t4", vals["gpu_type"], "GPUCount with no type must default to nvidia-tesla-t4")
		assert.Equal(t, 2, vals["gpu_count"])
	})

	t.Run("explicit GPUType + GPUCount preserved", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "n1-standard-4", GPUType: "nvidia-tesla-v100", GPUCount: 4})
		vals, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-tesla-v100", vals["gpu_type"])
		assert.Equal(t, 4, vals["gpu_count"])
	})

	t.Run("capitalised + padded GPUType normalizes and is emitted canonical", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "n1-standard-4", GPUType: "  NVIDIA-Tesla-T4  "})
		vals, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.NoError(t, err)
		// The mapper emits the canonical (lower-cased, trimmed) form so the
		// composed HCL is the type string GCP expects (#752 review, P2-2).
		assert.Equal(t, "nvidia-tesla-t4", vals["gpu_type"])
	})

	t.Run("no GPU leaves gpu_type/gpu_count unset", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "e2-medium", DiskSizeGb: 50})
		vals, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.NoError(t, err)
		_, hasType := vals["gpu_type"]
		_, hasCount := vals["gpu_count"]
		assert.False(t, hasType, "non-GPU compute must not set gpu_type")
		assert.False(t, hasCount, "non-GPU compute must not set gpu_count")
		assert.Equal(t, "e2-medium", vals["machine_type"], "non-GPU fields unchanged")
	})

	t.Run("bundled-GPU machine (G2) with explicit GPUType is rejected on a VM", func(t *testing.T) {
		// On a Compute VM the GPU is attached automatically by the machine type,
		// so an explicit guest_accelerator is invalid (unlike GKE — see the GKE
		// test below where the same family is ACCEPTED).
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "g2-standard-4", GPUType: "nvidia-l4"})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "attaches its GPU automatically")
	})

	t.Run("bundled-GPU machine (A2) with explicit GPUType is rejected on a VM", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "a2-highgpu-1g", GPUType: "nvidia-tesla-a100"})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "attaches its GPU automatically")
	})

	t.Run("invalid GPUCount on N1 is rejected", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "n1-standard-4", GPUType: "nvidia-tesla-t4", GPUCount: 3})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid GCP accelerator count")
	})

	t.Run("non-N1 non-bundled machine (E2) with a GPU is rejected", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "e2-standard-4", GPUType: "nvidia-tesla-t4"})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not accept one")
	})

	t.Run("GPU with no machine type is rejected (preset default is non-N1)", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{GPUType: "nvidia-tesla-t4"})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not accept one")
	})

	t.Run("unknown accelerator type on N1 is rejected", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "n1-standard-4", GPUType: "nvidia-l4"})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a known N1-attachable NVIDIA accelerator")
	})
}

// TestBuildModuleValues_GCPGKE_GPU exercises the gcp_gke GPU mapper branch
// (#767, #752 review). Unlike gcp_compute, a GKE node pool DECLARES the
// accelerator even for the accelerator-optimized families, so the bundled
// families (G2/A2/A3/...) are ACCEPTED here (paired with their GPU type), not
// rejected — while N1 attaches the T4/V100/P100/P4 accelerators.
func TestBuildModuleValues_GCPGKE_GPU(t *testing.T) {
	m := DefaultMapper{}

	t.Run("explicit GPUType on N1 emits gpu_type + default count", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "n1-standard-4", GPUType: "nvidia-tesla-t4"})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-tesla-t4", vals["gpu_type"])
		assert.Equal(t, 1, vals["gpu_count"])
	})

	t.Run("GPUCount only on N1 defaults the accelerator type", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "n1-standard-8", GPUCount: 2})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-tesla-t4", vals["gpu_type"])
		assert.Equal(t, 2, vals["gpu_count"])
	})

	t.Run("GPU config coexists with node_count and regional", func(t *testing.T) {
		f := false
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "n1-standard-4", NodeCount: "3", Regional: &f, GPUType: "nvidia-tesla-p100", GPUCount: 1})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-tesla-p100", vals["gpu_type"])
		assert.Equal(t, 1, vals["gpu_count"])
		assert.Equal(t, 3, vals["node_count"], "non-GPU fields still map")
		assert.Equal(t, false, vals["regional"])
	})

	t.Run("no GPU leaves gpu_type/gpu_count unset", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "e2-standard-4", NodeCount: "1"})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		_, hasType := vals["gpu_type"]
		_, hasCount := vals["gpu_count"]
		assert.False(t, hasType, "non-GPU node pool must not set gpu_type")
		assert.False(t, hasCount, "non-GPU node pool must not set gpu_count")
	})

	t.Run("bundled-GPU machine (G2) with matching GPUType is accepted", func(t *testing.T) {
		// The #752 review fix: G2 is a valid GKE GPU node pool (paired with
		// nvidia-l4), where the same config is rejected on a Compute VM.
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "g2-standard-4", GPUType: "nvidia-l4", GPUCount: 1})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-l4", vals["gpu_type"])
		assert.Equal(t, 1, vals["gpu_count"])
	})

	t.Run("bundled-GPU machine (A3) with matching GPUType is accepted", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "a3-highgpu-8g", GPUType: "nvidia-h100-80gb", GPUCount: 8})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-h100-80gb", vals["gpu_type"])
		assert.Equal(t, 8, vals["gpu_count"])
	})

	t.Run("bundled-GPU machine (G2) with no GPUType defaults to family accelerator", func(t *testing.T) {
		// GPUCount alone signals GPU intent; the mapper defaults the type to the
		// family's bundled accelerator (nvidia-l4 for G2), not the N1 T4 default.
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "g2-standard-4", GPUCount: 2})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-l4", vals["gpu_type"], "G2 with no GPUType must default to its bundled nvidia-l4")
		assert.Equal(t, 2, vals["gpu_count"])
	})

	t.Run("bundled-GPU machine (G2) with mismatched GPUType is rejected", func(t *testing.T) {
		// nvidia-tesla-a100 is an A2 accelerator — wrong pairing for G2.
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "g2-standard-4", GPUType: "nvidia-tesla-a100"})
		_, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not pair with machine_type")
	})

	t.Run("non-N1 non-bundled machine with a GPU is rejected", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "n2-standard-4", GPUType: "nvidia-tesla-t4"})
		_, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not accept one")
	})

	t.Run("unknown accelerator type on N1 is rejected", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "n1-standard-4", GPUType: "nvidia-h100-80gb"})
		_, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a known N1-attachable NVIDIA accelerator")
	})

	t.Run("invalid GPUCount is rejected", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "n1-standard-4", GPUType: "nvidia-tesla-t4", GPUCount: 5})
		_, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid GCP accelerator count")
	})

	t.Run("capitalised + padded GPUType normalizes and is emitted canonical", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "n1-standard-4", GPUType: "  NVIDIA-Tesla-T4  "})
		vals, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.NoError(t, err)
		assert.Equal(t, "nvidia-tesla-t4", vals["gpu_type"], "GKE must emit the canonical lower-cased type (#752 review, P2-2)")
	})
}
