package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// manifestFile is the on-disk file name written into --output-dir.
const manifestFile = "imported.json"

// writeManifest validates the resource set with composer.ValidateImportedResources
// and writes the JSON array of ImportedResource into <dir>/imported.json.
// Validation runs BEFORE the file is written so a failing validator never
// produces a half-good manifest on disk.
//
// Returns (path, count, error). On validator failure, error includes every
// issue code so the operator can correct in one round-trip rather than
// running discover repeatedly.
func writeManifest(dir, cloud string, resources []imported.ImportedResource) (string, int, error) {
	// json.MarshalIndent of a nil slice writes "null"; downstream
	// consumers (Reliable, Riley) cannot range over null. Force an empty
	// slice so the on-disk manifest is always a JSON array.
	if resources == nil {
		resources = []imported.ImportedResource{}
	}

	// Sort by Address so the on-disk file is byte-identical across runs
	// for the same input. Stage 1's adopt path took the same approach.
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Identity.Address < resources[j].Identity.Address
	})

	if issues := composer.ValidateImportedResources(cloud, resources); len(issues) > 0 {
		return "", 0, fmt.Errorf("manifest validation failed (%d issue(s)): %s", len(issues), formatIssues(issues))
	}

	body, err := json.MarshalIndent(resources, "", "  ")
	if err != nil {
		return "", 0, fmt.Errorf("marshal manifest: %w", err)
	}
	body = append(body, '\n')

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	out := filepath.Join(dir, manifestFile)
	if err := os.WriteFile(out, body, 0o644); err != nil {
		return "", 0, fmt.Errorf("write %s: %w", out, err)
	}
	return out, len(resources), nil
}

// formatIssues turns a slice of validation issues into a multi-line string
// suitable for an error message. Each line carries the issue code and a
// short reason — the operator does not need the full ValidationIssue
// fields (Field, Suggestion, etc.) at the CLI surface; those are for
// programmatic callers.
func formatIssues(issues []composer.ValidationIssue) string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		line := i.Code
		if i.Reason != "" {
			line += ": " + i.Reason
		}
		if i.Field != "" {
			line += " (field=" + i.Field + ")"
		}
		out = append(out, line)
	}
	// Sorted for deterministic output regardless of validator iteration order.
	sort.Strings(out)
	return "\n  " + strings.Join(out, "\n  ")
}
