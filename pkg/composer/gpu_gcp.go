package composer

import (
	"fmt"
	"strings"
)

// gpu_gcp.go centralises the single source of truth the GCP GPU mapper branches
// (#767) share: the default attachable accelerator type, the set of NVIDIA
// accelerator types that attach to N1 machines via guest_accelerator, the set of
// machine families that bundle a GPU with the machine type, and — for GKE only —
// the bundled-family → GPU-type pairing. Both the gcp/gke node-pool branch and
// the gcp/compute instance branch consume these so the knowledge can't drift
// between them, and a drift test (gpu_gcp_families_drift_test.go) asserts the Go
// sets stay in lockstep with the HCL `_gpu_attachable_accelerators` and
// `_gpu_bundled_machine_families` locals in gcp/compute/main.tf and
// gcp/gke/main.tf — the presets' own preconditions are the runtime SoT.
//
// GCP attaches GPUs in two distinct ways, and the validation differs by service
// (validate, don't mask — the #759 lesson applied to GCP). The crucial split is
// Compute Engine VMs vs. GKE node pools (#752 review):
//
//   - On a Compute Engine VM (google_compute_instance), only N1 general-purpose
//     machines accept an ATTACHED GPU via guest_accelerator (T4 / V100 / P100 /
//     P4). A2 / A3 / A4 / G2 / G4 accelerator-optimized machines have their GPU
//     ATTACHED AUTOMATICALLY by the machine type — you do NOT pass
//     guest_accelerator, and doing so is invalid. Every other family (E2, N2,
//     C3, ...) cannot take a GPU at all. Verified against
//     https://cloud.google.com/compute/docs/gpus ("For these machine types, the
//     GPU model is automatically attached to the instance") and the create-VM
//     guide (no --accelerator for A2/A3/G2; --accelerator required for N1).
//
//   - On a GKE node pool (google_container_node_pool), guest_accelerator IS
//     REQUIRED even for the accelerator-optimized families — the node pool must
//     declare the accelerator type+count that matches the machine type. g2 pairs
//     with nvidia-l4, a2 with nvidia-tesla-a100 / nvidia-a100-80gb, a3 with
//     nvidia-h100-80gb / nvidia-h200-141gb, etc. N1 node pools attach the same
//     N1-attachable accelerators as a VM. Verified against
//     https://cloud.google.com/kubernetes-engine/docs/how-to/gpus
//     ("gcloud container node-pools create ... --accelerator type=nvidia-l4 ...
//     --machine-type g2-standard-4"). This is the bug the #752 review caught:
//     the GKE path reused the Compute bundled-family REJECTION, which would have
//     rejected the very configs GKE GPU node pools require.
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

// validGCPGPUCounts is the set of accelerator counts GCP accepts across the
// N1-attachable GPU models (T4: 1/2/4, V100: 1/2/4/8, P100: 1/2/4, P4: 1/2/4)
// and the per-node counts the accelerator-optimized families expose (1/2/4/8/16
// depending on the machine type, e.g. a2-highgpu-8g = 8, a2-megagpu-16g = 16).
// We validate against this union rather than the full per-type/per-machine
// matrix: the exact legal count is type- AND zone-AND-machine-specific (a
// deploy-time / quota concern), but a count outside this set is always wrong and
// is worth catching at compose time. Verified against
// https://cloud.google.com/compute/docs/gpus.
var validGCPGPUCounts = map[int]struct{}{
	1:  {},
	2:  {},
	4:  {},
	8:  {},
	16: {},
}

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
// H200 / B200 / GB200 / GB300), G2 (L4), and G4 (RTX-PRO-6000).
//
// On Compute Engine VMs the GPU is attached automatically and a guest_accelerator
// is invalid, so the compute mapper rejects an explicit GPUType on these. On GKE
// node pools the accelerator IS declared (paired with the machine type), so the
// gke mapper ACCEPTS these families and validates the type/family pairing against
// gpuBundledFamilyAccelerators below.
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

// gpuBundledFamilyAccelerators maps each bundled-GPU machine family to the
// accelerator type(s) GKE pairs with it in the node-pool accelerator config.
// Used only by the GKE path: when a caller requests a GPU node pool on one of
// these families, the gpu_type must be one of the family's accelerators (or is
// defaulted to the first listed when unset). Verified against
// https://cloud.google.com/kubernetes-engine/docs/how-to/gpus.
//
// Keys MUST exactly match gpuBundledMachineFamilies — TestGCPGPUBundledPairings
// enforces that every bundled family has a pairing entry and vice-versa.
var gpuBundledFamilyAccelerators = map[string][]string{
	"a2":  {"nvidia-tesla-a100", "nvidia-a100-80gb"},
	"a3":  {"nvidia-h100-80gb", "nvidia-h100-mega-80gb", "nvidia-h200-141gb"},
	"a4":  {"nvidia-b200"},
	"a4x": {"nvidia-gb200", "nvidia-gb300"},
	"g2":  {"nvidia-l4"},
	"g4":  {"nvidia-rtx-pro-6000"},
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
// is bundled with the machine type (per gpuBundledMachineFamilies).
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

// normalizeGPUType lower-cases and trims a GPU type so the value the mapper
// emits is the canonical form GCP expects ("  NVIDIA-Tesla-T4 " → "nvidia-tesla-t4").
func normalizeGPUType(gpuType string) string {
	return strings.ToLower(strings.TrimSpace(gpuType))
}

// validateGCPGPUCount rejects an accelerator count GCP never accepts (per
// validGCPGPUCounts). component is the IR field name used in the message. A
// count of 0 is treated as "default to 1" by the caller and is not validated
// here — only an explicit positive count is checked.
func validateGCPGPUCount(component string, count int) error {
	if count <= 0 {
		return nil
	}
	if _, ok := validGCPGPUCounts[count]; !ok {
		return NewValidationError(fmt.Sprintf(
			"%s GPUCount=%d is not a valid GCP accelerator count (expected one of "+
				"1, 2, 4, 8, 16). The exact legal count is GPU-type/zone/machine-"+
				"specific (a deploy-time concern), but counts outside this set are "+
				"always rejected by GCP.",
			component, count,
		))
	}
	return nil
}

// validateGCPComputeGPU enforces the Compute Engine VM attach-a-GPU rules
// (#752 review). On a VM the GPU attaches via guest_accelerator ONLY on N1
// machines; A2/A3/A4/G2/G4 attach their GPU automatically by machine type and
// reject an explicit guest_accelerator, and every other family takes none. It
// validates, in order:
//
//  1. A bundled-GPU machine family (A2/A3/A4/G2/G4) with an explicit GPUType is
//     rejected — the GPU comes with the machine type, so attaching a separate
//     accelerator is invalid and GCP rejects it.
//  2. A non-N1 machine type that is not a bundled-GPU family cannot take a GPU at
//     all — rejected (only N1 attaches GPUs via guest_accelerator).
//  3. An explicit GPUType that is not a known N1-attachable accelerator is
//     rejected (typo, or a bundled-only / TPU type used by mistake).
//  4. An explicit GPUCount outside the valid GCP set is rejected.
//
// An empty machine type is treated as the non-N1 preset default and rejected
// with the same N1 guidance — the caller must pick an N1 machine to attach a GPU.
func validateGCPComputeGPU(machineType, gpuType string, gpuCount int) error {
	const component = "GCPCompute"
	if isBundledGPUMachine(machineType) {
		return NewValidationError(fmt.Sprintf(
			"%s machine_type=%q attaches its GPU automatically (A2/A3/A4/G2/G4 "+
				"families ship a fixed GPU with the machine type): on a Compute Engine "+
				"VM you do not set a guest_accelerator, so an explicit GPUType is "+
				"invalid. Clear GPUType/GPUCount and use the accelerator-optimized "+
				"machine type alone, or pick an N1 machine to attach %s.",
			component, machineType, gpuTypeForMsg(gpuType),
		))
	}
	if !isN1Machine(machineType) {
		return NewValidationError(fmt.Sprintf(
			"%s requests a GPU but machine_type=%q does not accept one: GCP attaches "+
				"GPUs via guest_accelerator only on N1 general-purpose machines "+
				"(e.g. n1-standard-4). Pick an N1 machine type to attach a GPU, or "+
				"use an accelerator-optimized machine type (a2-*/a3-*/g2-*) whose GPU "+
				"is attached automatically and set no GPUType.",
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
	return validateGCPGPUCount(component, gpuCount)
}

// validateGCPGKEGPU enforces the GKE node-pool attach-a-GPU rules (#752 review).
// Unlike a Compute VM, a GKE node pool DECLARES the accelerator even for the
// accelerator-optimized families — the node pool's accelerator config must pair
// the machine type with its GPU. It validates, in order:
//
//  1. An N1 machine accepts the N1-attachable accelerators (T4/V100/P100/P4); an
//     explicit GPUType that is not one is rejected.
//  2. A bundled-GPU machine family (A2/A3/A4/G2/G4) is ACCEPTED, but an explicit
//     GPUType must be one of the accelerators GKE pairs with that family (per
//     gpuBundledFamilyAccelerators) — a mismatched type is rejected. An empty
//     GPUType is allowed; the mapper defaults it to the family's accelerator.
//  3. Any other family cannot take a GPU at all — rejected.
//  4. An explicit GPUCount outside the valid GCP set is rejected.
//
// An empty machine type is treated as the non-N1 preset default and rejected
// with N1 guidance — the caller must pick an N1 or accelerator-optimized machine.
func validateGCPGKEGPU(machineType, gpuType string, gpuCount int) error {
	const component = "GCPGKE"
	fam := machineFamily(machineType)
	switch {
	case fam == "n1":
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
	case isBundledGPUMachine(machineType):
		if gpuType != "" && !isBundledFamilyAccelerator(fam, gpuType) {
			return NewValidationError(fmt.Sprintf(
				"%s GPUType=%q does not pair with machine_type=%q: a GKE %s node pool "+
					"declares the accelerator that matches the machine family (expected "+
					"one of %s). Clear GPUType to default to %s, or pick the matching "+
					"accelerator.",
				component, gpuType, machineType, strings.ToUpper(fam),
				strings.Join(gpuBundledFamilyAccelerators[fam], "/"),
				gpuBundledFamilyAccelerators[fam][0],
			))
		}
	default:
		return NewValidationError(fmt.Sprintf(
			"%s requests a GPU but machine_type=%q does not accept one: a GKE GPU node "+
				"pool needs an N1 machine (attaches T4/V100/P100/P4) or an accelerator-"+
				"optimized machine (a2-*/a3-*/a4-*/g2-*/g4-*, whose GPU is declared by "+
				"the matching accelerator type). Pick one of those machine types.",
			component, machineType,
		))
	}
	return validateGCPGPUCount(component, gpuCount)
}

// isBundledFamilyAccelerator reports whether gpuType is one of the accelerators
// GKE pairs with the given bundled machine family. gpuType is normalized before
// the lookup so case/whitespace don't matter.
func isBundledFamilyAccelerator(family, gpuType string) bool {
	want := normalizeGPUType(gpuType)
	for _, a := range gpuBundledFamilyAccelerators[family] {
		if a == want {
			return true
		}
	}
	return false
}

// defaultGKEGPUType returns the accelerator type the GKE mapper supplies when a
// caller requests a GPU node pool on machineType but does not pin a GPUType. For
// N1 it is the shared default (nvidia-tesla-t4); for a bundled family it is that
// family's first paired accelerator. Returns "" for a family that cannot take a
// GPU (validation rejects those before this is consulted).
func defaultGKEGPUType(machineType string) string {
	fam := machineFamily(machineType)
	if fam == "n1" {
		return defaultGCPAccelerator
	}
	if accs := gpuBundledFamilyAccelerators[fam]; len(accs) > 0 {
		return accs[0]
	}
	return ""
}

// gpuTypeForMsg renders the GPU type for an error message, falling back to "a
// GPU" when the caller signalled GPU intent via GPUCount only (empty GPUType).
func gpuTypeForMsg(gpuType string) string {
	if strings.TrimSpace(gpuType) == "" {
		return "a GPU"
	}
	return gpuType
}
