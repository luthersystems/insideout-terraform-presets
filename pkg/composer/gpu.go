package composer

import "strings"

// gpu.go centralises the single source of truth the GPU mapper branches
// (#759) share: the default GPU instance type and the set of NVIDIA x86_64
// EC2 instance families. Both the AWS EC2 and EKS-node-group branches consume
// these so the default + family knowledge can't drift between them, and a
// drift test (gpu_families_drift_test.go) asserts the Go set stays in lockstep
// with the HCL `_gpu_x86_families` list in aws/eks_nodegroup/main.tf — the
// preset's own AMI auto-derive is the runtime source of truth.

// defaultGPUInstanceType is the instance type the mapper supplies when a
// caller enables GPU but does not pin an explicit instance type. g5.xlarge is
// the cheapest single-A10G NVIDIA family and is shared by the EC2 and EKS GPU
// paths (and by pricing, so a GPU stack is not priced as the non-GPU default).
const defaultGPUInstanceType = "g5.xlarge"

// gpuX86Families is the allow-list of NVIDIA-GPU x86_64 EC2 instance families
// (#759). These are the families that require an x86_64 NVIDIA-bundled AMI so
// the node boots with the NVIDIA kernel driver + container runtime present.
//
// This list MUST stay in lockstep with the HCL `_gpu_x86_families` local in
// aws/eks_nodegroup/main.tf — TestGPUFamiliesDrift enforces it. Note g5g is
// deliberately ABSENT: it is Graviton (ARM) + NVIDIA T4G, and EKS has no ARM
// NVIDIA managed AMI type, so it stays on the ARM standard AMI path.
var gpuX86Families = map[string]struct{}{
	"g4dn": {},
	"g5":   {},
	"g6":   {},
	"g6e":  {},
	"gr6":  {},
	"p3":   {},
	"p3dn": {},
	"p4d":  {},
	"p4de": {},
	"p5":   {},
	"p5e":  {},
	"p5en": {},
}

// instanceFamily returns the family prefix of an EC2 instance type — the part
// before the first "." — matching the HCL `split(".", ...)[0]` derive. For
// "g5.xlarge" it returns "g5"; for a string with no "." it returns the input
// unchanged. The input is trimmed and lower-cased first so surrounding
// whitespace or a capitalised family ("G5.xlarge") still matches the allow-list
// (AWS instance types are canonically lower-case).
func instanceFamily(instanceType string) string {
	normalized := strings.ToLower(strings.TrimSpace(instanceType))
	return strings.SplitN(normalized, ".", 2)[0]
}

// isGPUX86Family reports whether instanceType belongs to a known NVIDIA-GPU
// x86_64 family (per gpuX86Families). Used to validate explicit GPU instance
// types so a non-GPU or ARM family is rejected at compose time instead of
// silently forcing an x86_64/NVIDIA AMI onto incompatible hardware.
func isGPUX86Family(instanceType string) bool {
	_, ok := gpuX86Families[instanceFamily(instanceType)]
	return ok
}
