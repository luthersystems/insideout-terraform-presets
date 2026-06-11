package composer

import (
	"fmt"
	"strings"
)

// gpu_gcp.go centralises the single source of truth the GCP GPU mapper branches
// (#767) share: the default attachable accelerator type, the set of NVIDIA
// accelerator types that attach to N1 machines via guest_accelerator, and the
// set of machine families that bundle a GPU with the machine type (so attaching
// a separate accelerator there is invalid). Both the gcp/gke node-pool branch
// and the gcp/compute instance branch consume these so the knowledge can't drift
// between them, and a drift test (gpu_gcp_families_drift_test.go) asserts the Go
// sets stay in lockstep with the HCL `_gpu_attachable_accelerators` and
// `_gpu_bundled_machine_families` locals in gcp/compute/main.tf and
// gcp/gke/main.tf — the presets' own preconditions are the runtime SoT.
//
// GCP attaches GPUs in two distinct ways, and the validation enforces the right
// one (validate, don't mask — the #759 lesson applied to GCP):
//
//   - N1 general-purpose machines accept ATTACHED GPUs via guest_accelerator
//     (T4 / V100 / P100 / P4). This is the path GPUType/GPUCount drive.
//   - A2 / A3 / A4 / G2 / G4 accelerator-optimized machines BUNDLE their GPU
//     with the machine type (A100 / H100 / H200 / B200 / L4 / RTX-PRO-6000).
//     Setting guest_accelerator there is rejected by GCP — the GPU is implied
//     by the machine type, so we reject an explicit GPUType for those families.
//   - Every other family (E2, N2, C3, ...) cannot take a GPU at all.
//
// GCP also forbids live-migration for any GPU-bearing instance, so the compute
// preset forces scheduling.on_host_maintenance = "TERMINATE" by construction
// when a GPU is attached (validate-don't-mask: the mapper never lets MIGRATE
// coexist with a GPU).

// defaultGCPAccelerator is the accelerator type the mapper supplies when a
// caller sets GPUCount on an N1 machine but does not pin an explicit GPUType.
// nvidia-tesla-t4 is the cheapest, most broadly available N1-attachable NVIDIA
// GPU and is shared by the GKE node-pool and Compute instance GPU paths.
const defaultGCPAccelerator = "nvidia-tesla-t4"

// defaultGCPGPUCount is the accelerator count the mapper supplies when a caller
// pins a GPUType on an N1 machine but leaves GPUCount unset (a single GPU).
const defaultGCPGPUCount = 1

// gpuAttachableAccelerators is the allow-list of NVIDIA accelerator types that
// attach to N1 machines via guest_accelerator (#767). vws variants are the
// virtual-workstation SKUs of the same silicon and attach the same way.
//
// This set MUST stay in lockstep with the HCL `_gpu_attachable_accelerators`
// locals in gcp/compute/main.tf and gcp/gke/main.tf — TestGCPGPUFamiliesDrift
// enforces it.
var gpuAttachableAccelerators = map[string]struct{}{
	"nvidia-tesla-t4":       {},
	"nvidia-tesla-t4-vws":   {},
	"nvidia-tesla-p4":       {},
	"nvidia-tesla-p4-vws":   {},
	"nvidia-tesla-v100":     {},
	"nvidia-tesla-p100":     {},
	"nvidia-tesla-p100-vws": {},
}

// gpuBundledMachineFamilies is the set of machine-type family prefixes whose
// GPU is BUNDLED with the machine type (#767): A2 / A3 / A4 / A4X (A100 / H100 /
// H200 / B200 / GB200 / GB300), G2 (L4), and G4 (RTX-PRO-6000). For these you
// select the GPU by picking the machine type; attaching a guest_accelerator is
// invalid and GCP rejects it. We reject an explicit GPUType on these families at
// compose time rather than silently attaching an incompatible accelerator.
//
// This set MUST stay in lockstep with the HCL `_gpu_bundled_machine_families`
// locals in gcp/compute/main.tf and gcp/gke/main.tf — TestGCPGPUFamiliesDrift
// enforces it.
var gpuBundledMachineFamilies = map[string]struct{}{
	"a2":  {},
	"a3":  {},
	"a4":  {},
	"a4x": {},
	"g2":  {},
	"g4":  {},
}

// machineFamily returns the family prefix of a GCP machine type — the part
// before the first "-" — matching the HCL `split("-", ...)[0]` derive. For
// "n1-standard-4" it returns "n1"; for "a2-highgpu-1g" it returns "a2". The
// input is trimmed and lower-cased first so surrounding whitespace or a
// capitalised family still matches (GCP machine types are canonically
// lower-case).
func machineFamily(machineType string) string {
	normalized := strings.ToLower(strings.TrimSpace(machineType))
	return strings.SplitN(normalized, "-", 2)[0]
}

// isN1Machine reports whether machineType is an N1 general-purpose machine, the
// only family that takes an ATTACHED GPU via guest_accelerator. An empty machine
// type is not N1 (the preset defaults to a non-N1 type), so an attached GPU with
// no machine type is rejected — the caller must pick an N1 machine.
func isN1Machine(machineType string) bool {
	return machineFamily(machineType) == "n1"
}

// isBundledGPUMachine reports whether machineType belongs to a family whose GPU
// is bundled with the machine type (per gpuBundledMachineFamilies), so attaching
// a separate guest_accelerator is invalid.
func isBundledGPUMachine(machineType string) bool {
	_, ok := gpuBundledMachineFamilies[machineFamily(machineType)]
	return ok
}

// isAttachableAccelerator reports whether gpuType is a known N1-attachable NVIDIA
// accelerator type (per gpuAttachableAccelerators). Used to validate an explicit
// GPUType so an unknown or bundled-only accelerator is rejected at compose time
// instead of producing an apply-time GCP error.
func isAttachableAccelerator(gpuType string) bool {
	_, ok := gpuAttachableAccelerators[strings.ToLower(strings.TrimSpace(gpuType))]
	return ok
}

// validateGCPGPU enforces the GCP attach-a-GPU rules shared by the gcp_compute
// and gcp_gke mapper branches (#767), so both reject the same invalid configs
// with identical, actionable error content. component is the IR field name used
// in the error message ("GCPCompute" / "GCPGKE"). It validates, in order:
//
//  1. A bundled-GPU machine family (A2/A3/A4/G2/G4) with an explicit GPUType is
//     rejected — the GPU comes with the machine type, so attaching a separate
//     accelerator is invalid and GCP rejects it.
//  2. A non-N1 machine type that is not a bundled-GPU family cannot take a GPU at
//     all — rejected (only N1 attaches GPUs via guest_accelerator).
//  3. An explicit GPUType that is not a known N1-attachable accelerator is
//     rejected (typo, or a bundled-only / TPU type used by mistake).
//
// An empty machine type is treated as the non-N1 preset default and rejected
// with the same N1 guidance — the caller must pick an N1 machine to attach a GPU.
func validateGCPGPU(component, machineType, gpuType string) error {
	if isBundledGPUMachine(machineType) {
		return NewValidationError(fmt.Sprintf(
			"%s machine_type=%q bundles its GPU with the machine type "+
				"(A2/A3/A4/G2/G4 families ship a fixed GPU): you select the GPU by "+
				"picking the machine type, so an explicit GPUType is invalid. "+
				"Clear GPUType/GPUCount and use the accelerator-optimized machine "+
				"type alone, or pick an N1 machine to attach %s.",
			component, machineType, gpuTypeForMsg(gpuType),
		))
	}
	if !isN1Machine(machineType) {
		return NewValidationError(fmt.Sprintf(
			"%s requests a GPU but machine_type=%q does not accept one: GCP attaches "+
				"GPUs via guest_accelerator only on N1 general-purpose machines "+
				"(e.g. n1-standard-4). Pick an N1 machine type to attach a GPU, or "+
				"use an accelerator-optimized machine type (a2-*/a3-*/g2-*) whose GPU "+
				"is bundled and set no GPUType.",
			component, machineType,
		))
	}
	if gpuType != "" && !isAttachableAccelerator(gpuType) {
		return NewValidationError(fmt.Sprintf(
			"%s GPUType=%q is not a known N1-attachable NVIDIA accelerator "+
				"(expected one of nvidia-tesla-t4/nvidia-tesla-t4-vws/"+
				"nvidia-tesla-p4/nvidia-tesla-p4-vws/nvidia-tesla-v100/"+
				"nvidia-tesla-p100/nvidia-tesla-p100-vws). Clear GPUType to default "+
				"to %s, or pick a supported accelerator.",
			component, gpuType, defaultGCPAccelerator,
		))
	}
	return nil
}

// gpuTypeForMsg renders the GPU type for an error message, falling back to "a
// GPU" when the caller signalled GPU intent via GPUCount only (empty GPUType).
func gpuTypeForMsg(gpuType string) string {
	if strings.TrimSpace(gpuType) == "" {
		return "a GPU"
	}
	return gpuType
}
