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

	t.Run("capitalised + padded GPUType normalizes and is accepted", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "n1-standard-4", GPUType: "  NVIDIA-Tesla-T4  "})
		vals, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.NoError(t, err)
		// The mapper passes the user's literal through to HCL; validation
		// normalizes case/whitespace before the allow-list check.
		assert.Equal(t, "  NVIDIA-Tesla-T4  ", vals["gpu_type"])
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

	t.Run("bundled-GPU machine (G2) with explicit GPUType is rejected", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "g2-standard-4", GPUType: "nvidia-l4"})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bundles its GPU with the machine type")
	})

	t.Run("bundled-GPU machine (A2) with explicit GPUType is rejected", func(t *testing.T) {
		cfg := configWithGCPCompute(gcpComputeCfgInput{MachineType: "a2-highgpu-1g", GPUType: "nvidia-tesla-a100"})
		_, err := m.BuildModuleValues(KeyGCPCompute, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bundles its GPU with the machine type")
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
// (#767): same N1-attachable-vs-bundled rule as gcp_compute, applied to the node
// pool accelerator config.
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

	t.Run("bundled-GPU machine (A3) with explicit GPUType is rejected", func(t *testing.T) {
		cfg := configWithGCPGKE(gcpGKECfgInput{MachineType: "a3-highgpu-8g", GPUType: "nvidia-h100-80gb"})
		_, err := m.BuildModuleValues(KeyGCPGKE, nil, cfg, "", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bundles its GPU with the machine type")
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
}
