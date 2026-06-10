package composer

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGPUFamiliesDrift asserts the Go NVIDIA-GPU x86 family allow-list
// (gpuX86Families in gpu.go) stays in lockstep with the HCL `_gpu_x86_families`
// local in aws/eks_nodegroup/main.tf (#759). The HCL list drives the preset's
// AMI auto-derive (the runtime source of truth); the Go set drives mapper
// validation of explicit GPU instance types. If the two drift, a family
// accepted by the mapper might not derive an NVIDIA AMI (or vice versa), so a
// GPU node could come up without /dev/nvidia* — exactly the failure the
// validation exists to prevent. This test fails the moment either list adds or
// removes a family without the other.
func TestGPUFamiliesDrift(t *testing.T) {
	mainTF := filepath.Join("..", "..", "aws", "eks_nodegroup", "main.tf")
	b, err := os.ReadFile(mainTF)
	if err != nil {
		t.Fatalf("read %s: %v", mainTF, err)
	}

	hclFamilies := parseGPUFamiliesHCL(t, string(b))
	if len(hclFamilies) == 0 {
		t.Fatalf("no families parsed from _gpu_x86_families in %s — parser or HCL shape changed", mainTF)
	}

	goFamilies := map[string]struct{}{}
	for f := range gpuX86Families {
		goFamilies[f] = struct{}{}
	}

	// Families in HCL but missing from the Go set.
	var missingInGo []string
	for f := range hclFamilies {
		if _, ok := goFamilies[f]; !ok {
			missingInGo = append(missingInGo, f)
		}
	}
	// Families in the Go set but missing from HCL.
	var missingInHCL []string
	for f := range goFamilies {
		if _, ok := hclFamilies[f]; !ok {
			missingInHCL = append(missingInHCL, f)
		}
	}

	sort.Strings(missingInGo)
	sort.Strings(missingInHCL)
	if len(missingInGo) > 0 {
		t.Errorf("families in aws/eks_nodegroup/main.tf _gpu_x86_families but missing from gpuX86Families (gpu.go): %v", missingInGo)
	}
	if len(missingInHCL) > 0 {
		t.Errorf("families in gpuX86Families (gpu.go) but missing from aws/eks_nodegroup/main.tf _gpu_x86_families: %v", missingInHCL)
	}
}

// parseGPUFamiliesHCL extracts the quoted family strings from the
// `_gpu_x86_families = [ ... ]` local assignment. It isolates the bracketed
// list body and then pulls every double-quoted token, so reordering or
// reflowing the HCL doesn't affect the result.
func parseGPUFamiliesHCL(t *testing.T, hcl string) map[string]struct{} {
	t.Helper()
	listRe := regexp.MustCompile(`(?s)_gpu_x86_families\s*=\s*\[(.*?)\]`)
	m := listRe.FindStringSubmatch(hcl)
	if m == nil {
		t.Fatalf("could not locate `_gpu_x86_families = [...]` in main.tf")
	}
	body := m[1]
	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	out := map[string]struct{}{}
	for _, tm := range tokenRe.FindAllStringSubmatch(body, -1) {
		out[strings.TrimSpace(tm[1])] = struct{}{}
	}
	return out
}
