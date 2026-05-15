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

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// manifestFile is the on-disk file name written into --output-dir.
const manifestFile = "imported.json"

// unsupportedManifestFile is the on-disk file name for the broad-
// enumeration sibling manifest written by writeUnsupportedManifest
// when --include-unsupported is set (#296).
const unsupportedManifestFile = "unsupported.json"

// graphManifestFile is the on-disk file name for the dependency-graph
// sibling manifest written by writeGraphManifest after the depchase
// loop converges (#297). The reliable wizard's resource picker reads
// graph.json to close the auto-include loop: when an operator selects
// a resource, the wizard auto-includes every transitive `dependsOn`
// target. Empty edge list still emits `[]` (never null) so the picker
// never has to special-case missing/empty file.
const graphManifestFile = "graph.json"

// summaryManifestFile is the on-disk file name for the DiscoverySummary
// aggregate written by writeSummary at the end of every discover run
// (#298). The reliable wizard's discovery-review screen reads this file
// directly rather than recomputing the buckets client-side over a
// potentially large imported.json. Always emitted (no flag); empty
// input still produces a valid `{ total: 0, by_type: {}, ... }` body.
const summaryManifestFile = "summary.json"

// writeFileAtomic writes body to path via a same-directory <path>.tmp
// temp file, fsyncs the temp's fd, and renames into place. The rename
// is atomic on POSIX filesystems within the same directory — partial
// writes (process crash, disk full mid-write, SIGKILL) leave at most
// the .tmp file behind, never a half-written destination. All four
// sibling-manifest writers (writeManifest, writeUnsupportedManifest,
// writeGraphManifest, writeSummary) share this helper so the
// crash-safety guarantee is uniform across imported.json + the three
// sibling files. A regression that bypassed this helper for one of
// the four would re-introduce the race.
//
// On any error the temp file is removed (best-effort) so a failed
// write does not leave dangling .tmp artifacts cluttering the output
// directory across retries.
func writeFileAtomic(path string, body []byte, perm os.FileMode) (rerr error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open temp %s: %w", tmp, err)
	}
	defer func() {
		if rerr != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync temp %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

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
	// consumers (Reliable, the interactive agent) cannot range over null.
	// Force an empty slice so the on-disk manifest is always a JSON array.
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
	if err := writeFileAtomic(out, body, 0o644); err != nil {
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
// (Reliable, the interactive agent, the depchase loop) cannot range over null.
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

// writeUnsupportedManifest writes the wrapper-object form of
// unsupported.json (#309 wire-format break) into <dir>/unsupported.json.
// The pre-#309 shape was a bare JSON array; the cap-and-warn contract
// (--max-unsupported-results) needs metadata at the top level so the
// reliable wizard can render the "Showing first N of many" banner.
//
// Wire shape (with truncated=true, max_results=10):
//
//	{"resources":[…sorted rows…],"truncated":true,"max_results":10}
//
// Mirrors writeManifest's invariants on the inner Resources slice:
// nil → []-not-null, deterministic sort, file written atomically via
// writeFileAtomic. Sort key is (Type, Region, ID) — byte-identical
// output across runs for the same input. Unlike writeManifest no
// validator runs here; the carrier has no IR-side schema to enforce.
//
// Returns (path, count, error). count is the post-truncation row count
// (the size of resources passed in — the searcher already truncated
// before this is called). On marshal/write failure no file is written
// so the caller's stderr surface is the only side-effect of a soft-
// failure path (#296 contract: imported.json must complete even when
// unsupported emission fails).
func writeUnsupportedManifest(dir string, resources []UnsupportedResource, truncated bool, maxResults int) (string, int, error) {
	if resources == nil {
		resources = []UnsupportedResource{}
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Type != resources[j].Type {
			return resources[i].Type < resources[j].Type
		}
		if resources[i].Region != resources[j].Region {
			return resources[i].Region < resources[j].Region
		}
		return resources[i].ID < resources[j].ID
	})

	manifest := UnsupportedManifest{
		Resources:  resources,
		Truncated:  truncated,
		MaxResults: maxResults,
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", 0, fmt.Errorf("marshal unsupported manifest: %w", err)
	}
	body = append(body, '\n')

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	out := filepath.Join(dir, unsupportedManifestFile)
	if err := writeFileAtomic(out, body, 0o644); err != nil {
		return "", 0, fmt.Errorf("write %s: %w", out, err)
	}
	return out, len(resources), nil
}

// writeGraphManifest writes the JSON array of (from, to) GraphEdge
// rows into <dir>/graph.json (#297). Mirrors writeUnsupportedManifest's
// invariants: nil → `[]` (never `null`), deterministic sort by
// (From, To), file written via WriteFile (no temp+rename).
//
// Sort key is (From, To) — the same key the depchase Run loop sorts
// Edges with on insertion. We re-sort here so a caller that
// constructs a GraphEdge slice by hand and writes it through this
// function still produces byte-identical output across runs.
//
// Returns (path, count, error). The CLI treats graph.json as
// best-effort UI metadata: a write failure surfaces as a stderr WARN
// and does not abort the run (imported.json is the source of truth).
//
// Why graph.json is a sibling of imported.json (rather than
// embedded): the wizard picker reads them separately on different
// stages of the import flow, and an embedded edges slice would force
// every imported.json reader (composer, validators, the interactive
// agent, etc.) to know the dependency-graph schema. Persisting as a sibling keeps
// imported.json's wire shape unchanged and lets graph.json evolve
// independently.
func writeGraphManifest(dir string, edges []depchase.GraphEdge) (string, int, error) {
	if edges == nil {
		edges = []depchase.GraphEdge{}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	body, err := json.MarshalIndent(edges, "", "  ")
	if err != nil {
		return "", 0, fmt.Errorf("marshal graph manifest: %w", err)
	}
	body = append(body, '\n')

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	out := filepath.Join(dir, graphManifestFile)
	if err := writeFileAtomic(out, body, 0o644); err != nil {
		return "", 0, fmt.Errorf("write %s: %w", out, err)
	}
	return out, len(edges), nil
}

// writeSummary writes the DiscoverySummary aggregate into
// <dir>/summary.json (#298). Mirrors writeGraphManifest's invariants:
// nil/empty input still produces a structurally valid body (Total=0,
// every map serialized as `{}` not `null`, every slice serialized as
// `[]` not `null`); the file is written via WriteFile (no temp+rename);
// determinism comes from Go's encoding/json marshalling map keys in
// sorted order plus the SummarizeResources caller's deterministic
// inputs.
//
// Returns (path, error). The CLI treats summary.json as best-effort UI
// metadata: a write failure surfaces as a stderr WARN and does not
// abort the run (imported.json is the source of truth). The discovery-
// review screen falls back to client-side computation over
// imported.json if summary.json is missing.
//
// Why summary.json is a sibling of imported.json (rather than embedded):
// the aggregate's wire shape evolves independently of the per-resource
// IR — adding a new aggregate bucket (e.g. byCloud) shouldn't require
// every imported.json reader (composer, validators, the interactive
// agent) to know the summary schema. Keeping it as a sibling matches the same rationale
// behind unsupported.json (#296) and graph.json (#297).
func writeSummary(dir string, summary imported.DiscoverySummary) (string, error) {
	body, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal summary: %w", err)
	}
	body = append(body, '\n')

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	out := filepath.Join(dir, summaryManifestFile)
	if err := writeFileAtomic(out, body, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", out, err)
	}
	return out, nil
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
