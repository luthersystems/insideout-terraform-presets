package composer

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestGCPGPUFamiliesDrift asserts the Go bundled-GPU machine-family set
// (gpuBundledMachineFamilies in gpu_gcp.go) stays in lockstep with the HCL
// `_gpu_bundled_machine_families` locals in BOTH gcp/compute/main.tf and
// gcp/gke/main.tf (#767). The HCL lists drive each preset's plan-time GPU
// machine-type precondition; the Go set drives the composer's compose-time
// rejection of an explicit GPUType on a bundled-GPU machine. If any of the three
// drift, a machine family rejected by one layer might be accepted by another, so
// a config that composes could fail at apply (or vice-versa) — exactly the
// failure the validation exists to prevent. This fails the moment any list adds
// or removes a family without the other two.
//
// The N1-attachable accelerator allow-list (gpuAttachableAccelerators) is
// deliberately Go-only: the presets attach whatever gpu_type the composer passes
// and gate it with the machine-family precondition, so there is no HCL
// accelerator list to drift against.
func TestGCPGPUFamiliesDrift(t *testing.T) {
	goFamilies := map[string]struct{}{}
	for f := range gpuBundledMachineFamilies {
		goFamilies[f] = struct{}{}
	}

	for _, src := range []struct {
		name string
		path string
	}{
		{"compute", filepath.Join("..", "..", "gcp", "compute", "main.tf")},
		{"gke", filepath.Join("..", "..", "gcp", "gke", "main.tf")},
	} {
		t.Run(src.name, func(t *testing.T) {
			b, err := os.ReadFile(src.path)
			if err != nil {
				t.Fatalf("read %s: %v", src.path, err)
			}

			hclFamilies := parseGCPBundledFamiliesHCL(t, string(b))
			if len(hclFamilies) == 0 {
				t.Fatalf("no families parsed from _gpu_bundled_machine_families in %s — parser or HCL shape changed", src.path)
			}

			var missingInGo []string
			for f := range hclFamilies {
				if _, ok := goFamilies[f]; !ok {
					missingInGo = append(missingInGo, f)
				}
			}
			var missingInHCL []string
			for f := range goFamilies {
				if _, ok := hclFamilies[f]; !ok {
					missingInHCL = append(missingInHCL, f)
				}
			}

			sort.Strings(missingInGo)
			sort.Strings(missingInHCL)
			if len(missingInGo) > 0 {
				t.Errorf("families in %s _gpu_bundled_machine_families but missing from gpuBundledMachineFamilies (gpu_gcp.go): %v", src.path, missingInGo)
			}
			if len(missingInHCL) > 0 {
				t.Errorf("families in gpuBundledMachineFamilies (gpu_gcp.go) but missing from %s _gpu_bundled_machine_families: %v", src.path, missingInHCL)
			}
		})
	}
}

// parseGCPBundledFamiliesHCL extracts the quoted family strings from the
// `_gpu_bundled_machine_families = [ ... ]` local assignment, mirroring
// parseGPUFamiliesHCL (the AWS analogue). It isolates the bracketed list body,
// strips inline `#` comments, then pulls every double-quoted token so reordering
// or reflowing the HCL doesn't affect the result.
func parseGCPBundledFamiliesHCL(t *testing.T, hcl string) map[string]struct{} {
	t.Helper()
	listRe := regexp.MustCompile(`(?s)_gpu_bundled_machine_families\s*=\s*\[(.*?)\]`)
	m := listRe.FindStringSubmatch(hcl)
	if m == nil {
		t.Fatalf("could not locate `_gpu_bundled_machine_families = [...]` in main.tf")
	}
	body := stripHCLLineComments(m[1])
	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	out := map[string]struct{}{}
	for _, tm := range tokenRe.FindAllStringSubmatch(body, -1) {
		out[strings.TrimSpace(tm[1])] = struct{}{}
	}
	return out
}
