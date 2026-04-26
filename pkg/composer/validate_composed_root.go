package composer

import (
	"sort"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ValidateComposedRoot re-parses every emitted .tf and .tfvars file in the
// composed Files map and surfaces parse errors as ValidationIssues. Catches
// the "templating bug produced malformed HCL" class before terraform init
// does — a different failure surface than the per-preset terraform validate
// CI gate, which only ever sees the source presets, not the composed root.
func ValidateComposedRoot(files Files) []ValidationIssue {
	paths := make([]string, 0, len(files))
	for p := range files {
		if !isHCLFile(p) {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var issues []ValidationIssue
	for _, p := range paths {
		_, diags := hclsyntax.ParseConfig(files[p], p, hcl.InitialPos)
		if !diags.HasErrors() {
			continue
		}
		for _, d := range diags {
			if d.Severity != hcl.DiagError {
				continue
			}
			issues = append(issues, ValidationIssue{
				Field:  "composed_root." + strings.TrimPrefix(p, "/"),
				Code:   "hcl_parse_error",
				Reason: d.Error(),
			})
		}
	}
	return issues
}

func isHCLFile(p string) bool {
	lp := strings.ToLower(p)
	// .auto.tfvars is covered by the .tfvars check.
	return strings.HasSuffix(lp, ".tf") || strings.HasSuffix(lp, ".tfvars")
}
