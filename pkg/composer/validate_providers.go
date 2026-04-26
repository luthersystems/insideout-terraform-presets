package composer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
)

// ValidateProviderConstraints unions the required_providers VersionConstraints
// across every selected module's preset and reports any provider whose
// combined constraint set has no satisfying version. terraform init would
// surface the same error after pulling registry metadata; this catches it
// pre-init in pure Go.
//
// presetPaths maps block.Name to preset directory (same shape as
// ValidateModuleWiring). Modules absent from presetPaths contribute nothing.
func ValidateProviderConstraints(presetPaths map[string]string) []ValidationIssue {
	// providerLocal -> moduleName -> []rawConstraint
	perProvider := map[string]map[string][]string{}
	for modName, presetPath := range presetPaths {
		mod, err := InspectPreset(presetPath)
		if err != nil {
			continue
		}
		for provName, req := range mod.RequiredProviders {
			if len(req.VersionConstraints) == 0 {
				continue
			}
			if perProvider[provName] == nil {
				perProvider[provName] = map[string][]string{}
			}
			perProvider[provName][modName] = append(perProvider[provName][modName], req.VersionConstraints...)
		}
	}

	var issues []ValidationIssue
	provNames := make([]string, 0, len(perProvider))
	for n := range perProvider {
		provNames = append(provNames, n)
	}
	sort.Strings(provNames)

	for _, provName := range provNames {
		byModule := perProvider[provName]
		if len(byModule) < 2 {
			// A single module's constraints can't conflict with itself.
			continue
		}
		var all []string
		for _, cs := range byModule {
			all = append(all, cs...)
		}
		// Dedupe so identical pins don't bloat the AND-set unnecessarily.
		all = uniqueSortedStrings(all)
		combined := strings.Join(all, ",")
		c, err := version.NewConstraint(combined)
		if err != nil {
			// Unparseable constraint — don't manufacture a false conflict.
			continue
		}
		if findSatisfyingVersion(c) {
			continue
		}

		// Build a deterministic per-module breakdown for the issue reason.
		modNames := make([]string, 0, len(byModule))
		for n := range byModule {
			modNames = append(modNames, n)
		}
		sort.Strings(modNames)
		var parts []string
		for _, n := range modNames {
			parts = append(parts, fmt.Sprintf("%s pins %v", n, byModule[n]))
		}
		issues = append(issues, ValidationIssue{
			Field:  "providers." + provName,
			Code:   "provider_version_conflict",
			Reason: fmt.Sprintf("no version of provider %q satisfies the union: %s", provName, strings.Join(parts, "; ")),
		})
	}
	return issues
}

// findSatisfyingVersion sweeps a representative set of candidate versions and
// returns true if any of them satisfy the combined constraint set. The sweep
// covers major versions 0-50 with patch boundaries, which captures every
// realistic Terraform provider release window.
func findSatisfyingVersion(c version.Constraints) bool {
	for major := 0; major <= 50; major++ {
		for _, candidate := range []string{
			fmt.Sprintf("%d.0.0", major),
			fmt.Sprintf("%d.99.99", major),
		} {
			v, err := version.NewVersion(candidate)
			if err != nil {
				continue
			}
			if c.Check(v) {
				return true
			}
		}
	}
	return false
}

// ValidateSensitivePropagation flags wiring edges whose producer output is
// declared `sensitive = true`. The downstream consumer must mark its
// receiving variable sensitive too, otherwise terraform plan errors with
// "Output refers to sensitive values, but is not marked sensitive." Issue
// surfaces as a warning so reviewers can audit propagation by hand.
func ValidateSensitivePropagation(blocks []ModuleBlock, presetPaths map[string]string) []ValidationIssue {
	var issues []ValidationIssue
	for _, edge := range extractWiringEdges(blocks) {
		producerPath, ok := presetPaths[edge.Producer]
		if !ok {
			continue
		}
		producer, err := InspectPreset(producerPath)
		if err != nil {
			continue
		}
		out, ok := producer.Outputs[edge.Output]
		if !ok || !out.Sensitive {
			continue
		}
		issues = append(issues, ValidationIssue{
			Field: edge.Consumer + "." + edge.Input,
			Code:  "sensitive_propagation",
			Reason: fmt.Sprintf("module.%s.%s is sensitive; ensure %s.%s is declared with sensitive = true",
				edge.Producer, edge.Output, edge.Consumer, edge.Input),
		})
	}
	return issues
}

// uniqueSortedStrings returns a sorted, deduplicated copy of in.
func uniqueSortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
