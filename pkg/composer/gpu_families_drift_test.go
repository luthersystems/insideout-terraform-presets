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
// locals in BOTH aws/eks_nodegroup/main.tf and aws/ec2/main.tf (#759). The HCL
// lists drive each preset's plan-time GPU guard (the eks_nodegroup AMI
// auto-derive and the ec2 aws_instance precondition); the Go set drives mapper
// validation of explicit GPU instance types. If any of the three drift, a
// family accepted by one layer might be rejected (or mis-AMI'd) by another, so
// a GPU node could come up without /dev/nvidia* — exactly the failure the
// validation exists to prevent. This test fails the moment any list adds or
// removes a family without the other two.
func TestGPUFamiliesDrift(t *testing.T) {
	goFamilies := map[string]struct{}{}
	for f := range gpuX86Families {
		goFamilies[f] = struct{}{}
	}

	for _, src := range []struct {
		name string
		path string
	}{
		{"eks_nodegroup", filepath.Join("..", "..", "aws", "eks_nodegroup", "main.tf")},
		{"ec2", filepath.Join("..", "..", "aws", "ec2", "main.tf")},
	} {
		t.Run(src.name, func(t *testing.T) {
			b, err := os.ReadFile(src.path)
			if err != nil {
				t.Fatalf("read %s: %v", src.path, err)
			}

			hclFamilies := parseGPUFamiliesHCL(t, string(b))
			if len(hclFamilies) == 0 {
				t.Fatalf("no families parsed from _gpu_x86_families in %s — parser or HCL shape changed", src.path)
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
				t.Errorf("families in %s _gpu_x86_families but missing from gpuX86Families (gpu.go): %v", src.path, missingInGo)
			}
			if len(missingInHCL) > 0 {
				t.Errorf("families in gpuX86Families (gpu.go) but missing from %s _gpu_x86_families: %v", src.path, missingInHCL)
			}
		})
	}
}

// parseGPUFamiliesHCL extracts the quoted family strings from the
// `_gpu_x86_families = [ ... ]` local assignment. It isolates the bracketed
// list body, strips any `# ...` inline comments (so a quoted token sitting in a
// comment can't false-fail the drift check), and then pulls every double-quoted
// token, so reordering or reflowing the HCL doesn't affect the result.
func parseGPUFamiliesHCL(t *testing.T, hcl string) map[string]struct{} {
	t.Helper()
	listRe := regexp.MustCompile(`(?s)_gpu_x86_families\s*=\s*\[(.*?)\]`)
	m := listRe.FindStringSubmatch(hcl)
	if m == nil {
		t.Fatalf("could not locate `_gpu_x86_families = [...]` in main.tf")
	}
	body := stripHCLLineComments(m[1])
	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	out := map[string]struct{}{}
	for _, tm := range tokenRe.FindAllStringSubmatch(body, -1) {
		out[strings.TrimSpace(tm[1])] = struct{}{}
	}
	return out
}

// stripHCLLineComments removes `# ...`-to-end-of-line comments from each line of
// the bracket body so a quoted token appearing in an inline comment is not
// counted as a family. Only `#` comments are handled — the families list uses
// no `//` or block comments — and this is a deliberately simple line-oriented
// strip (the list body never contains a literal `#` inside a quoted token).
func stripHCLLineComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}
