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

// TestGCPGPUBundledPairings asserts the Go bundled-family → accelerator pairing
// map (gpuBundledFamilyAccelerators, GKE-only) is internally consistent and stays
// in lockstep with the HCL `_gpu_bundled_family_accelerator_map` in gcp/gke/main.tf
// (#752 review). Two contracts:
//
//  1. Every bundled machine family (gpuBundledMachineFamilies) has a pairing entry
//     and vice-versa — a family with no accelerator could never compose a GKE GPU
//     node pool, and a stray pairing entry would never be reachable.
//  2. The Go pairing map matches the GKE HCL map exactly (family → set of GPU
//     types). If they drift, a gpu_type the composer accepts for a family could be
//     rejected by the preset's pairing precondition at apply (or vice-versa) —
//     exactly the failure the split validation exists to prevent.
//
// The pairing map is GKE-only: on a Compute VM the accelerator is attached
// automatically and the compute preset rejects an explicit gpu_type on these
// families, so there is no compute HCL pairing map to drift against.
func TestGCPGPUBundledPairings(t *testing.T) {
	// (1) keys of the pairing map == the bundled-family set, and no family maps
	// to an empty accelerator list.
	for fam := range gpuBundledMachineFamilies {
		accs, ok := gpuBundledFamilyAccelerators[fam]
		if !ok {
			t.Errorf("bundled family %q has no accelerator pairing in gpuBundledFamilyAccelerators", fam)
			continue
		}
		if len(accs) == 0 {
			t.Errorf("bundled family %q maps to an empty accelerator list", fam)
		}
	}
	for fam := range gpuBundledFamilyAccelerators {
		if _, ok := gpuBundledMachineFamilies[fam]; !ok {
			t.Errorf("gpuBundledFamilyAccelerators has pairing for %q but it is not a bundled machine family", fam)
		}
	}

	// (2) the Go pairing map matches the GKE HCL `_gpu_bundled_family_accelerator_map`.
	path := filepath.Join("..", "..", "gcp", "gke", "main.tf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	hclMap := parseGCPBundledFamilyAcceleratorMapHCL(t, string(b))
	if len(hclMap) == 0 {
		t.Fatalf("no entries parsed from _gpu_bundled_family_accelerator_map in %s — parser or HCL shape changed", path)
	}

	for fam, goAccs := range gpuBundledFamilyAccelerators {
		hclAccs, ok := hclMap[fam]
		if !ok {
			t.Errorf("family %q in gpuBundledFamilyAccelerators (gpu_gcp.go) but missing from %s _gpu_bundled_family_accelerator_map", fam, path)
			continue
		}
		want := append([]string(nil), goAccs...)
		got := append([]string(nil), hclAccs...)
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(want, ",") != strings.Join(got, ",") {
			t.Errorf("family %q accelerator pairing drift: gpu_gcp.go=%v, %s=%v", fam, want, path, got)
		}
	}
	for fam := range hclMap {
		if _, ok := gpuBundledFamilyAccelerators[fam]; !ok {
			t.Errorf("family %q in %s _gpu_bundled_family_accelerator_map but missing from gpuBundledFamilyAccelerators (gpu_gcp.go)", fam, path)
		}
	}
}

// parseGCPBundledFamilyAcceleratorMapHCL extracts the
// `_gpu_bundled_family_accelerator_map = { fam = [ ... ] ... }` local from the
// GKE main.tf into a family → []accelerator map. It isolates the braced map body
// (balanced-brace scan from the first "{" after the assignment), strips inline
// comments, then matches each `key = [ "a", "b" ]` entry.
func parseGCPBundledFamilyAcceleratorMapHCL(t *testing.T, hcl string) map[string][]string {
	t.Helper()
	anchor := regexp.MustCompile(`_gpu_bundled_family_accelerator_map\s*=\s*\{`)
	loc := anchor.FindStringIndex(hcl)
	if loc == nil {
		t.Fatalf("could not locate `_gpu_bundled_family_accelerator_map = {` in main.tf")
	}
	// Balanced-brace scan starting at the opening brace.
	open := loc[1] - 1
	depth := 0
	end := -1
	for i := open; i < len(hcl); i++ {
		switch hcl[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("unbalanced braces in `_gpu_bundled_family_accelerator_map`")
	}
	body := stripHCLLineComments(hcl[open+1 : end])

	entryRe := regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)\s*=\s*\[([^\]]*)\]`)
	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	out := map[string][]string{}
	for _, em := range entryRe.FindAllStringSubmatch(body, -1) {
		fam := strings.TrimSpace(em[1])
		var accs []string
		for _, tm := range tokenRe.FindAllStringSubmatch(em[2], -1) {
			accs = append(accs, strings.TrimSpace(tm[1]))
		}
		out[fam] = accs
	}
	return out
}
