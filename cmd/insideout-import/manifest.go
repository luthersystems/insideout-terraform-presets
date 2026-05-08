package main

import (
	"bytes"
	"encoding/json"
	"errors"
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

// readManifest reads <path> from disk, decodes the JSON array of
// ImportedResource, and re-runs the same composer.ValidateImportedResources
// gate that writeManifest applied before persisting. It is the inverse of
// writeManifest, intended for the --from-manifest re-import path (#292) so a
// previously-discovered set can be replayed through Stage 2b/2c without
// re-walking the cloud.
//
// On malformed JSON the error includes the byte offset surfaced by
// json.SyntaxError / json.UnmarshalTypeError so an operator editing the
// file by hand can jump to the failing position. A literal `null` top-level
// is rejected explicitly: writeManifest's invariant is that an empty
// resource set serializes as `[]`, never `null`, and downstream consumers
// (Reliable, Riley, the depchase loop) cannot range over null.
//
// Returns ([]ImportedResource, nil) on success — never a nil slice with no
// error: an empty manifest decodes to a zero-length slice.
func readManifest(path, cloud string) ([]imported.ImportedResource, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Reject literal "null" before json.Unmarshal — Unmarshal of "null"
	// into a slice succeeds with a nil slice, which would silently pass
	// validation (validator returns nil on empty input). Explicit error
	// references the wire-shape contract that writeManifest enforces.
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return nil, fmt.Errorf("manifest %s: top-level JSON `null` is not a valid manifest (writeManifest emits `[]` for empty input; refusing to treat null as empty)", path)
	}

	var resources []imported.ImportedResource
	if err := json.Unmarshal(body, &resources); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxErr):
			return nil, fmt.Errorf("manifest %s: JSON syntax error at byte offset %d: %w", path, syntaxErr.Offset, err)
		case errors.As(err, &typeErr):
			return nil, fmt.Errorf("manifest %s: JSON type mismatch at byte offset %d (field %q, expected %s, got %s): %w", path, typeErr.Offset, typeErr.Field, typeErr.Type, typeErr.Value, err)
		default:
			return nil, fmt.Errorf("manifest %s: decode: %w", path, err)
		}
	}

	if issues := composer.ValidateImportedResources(cloud, resources); len(issues) > 0 {
		return nil, fmt.Errorf("manifest validation failed (%d issue(s)): %s", len(issues), formatIssues(issues))
	}
	return resources, nil
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
