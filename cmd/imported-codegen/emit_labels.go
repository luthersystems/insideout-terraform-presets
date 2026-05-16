package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/labels"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// labelEntry is one row of the labels subcommand's output. The JSON
// tags are lowerCamelCase so the emitted JSON matches the shape
// downstream consumers (TS UIs) expect verbatim — no per-consumer
// rename pass required.
type labelEntry struct {
	Label   string `json:"label"`
	IconKey string `json:"iconKey"`
}

// runLabels is the `labels` subcommand: emit a deterministic JSON
// object mapping every supported TF type (AWS ∪ GCP) to its display
// label and icon key. Falls back to labels.Label / labels.IconKey's
// default rule for types without a curated override, so the emitted
// map covers the full registry surface even when the override map is
// empty (the skeleton state). Output is sorted by key for golden-file
// stability.
//
// Default destination is stdout; --output <path> writes to a file.
func runLabels(args []string) int {
	fs := flag.NewFlagSet("labels", flag.ExitOnError)
	out := fs.String("output", "", "path to write JSON to (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return writeJSONOutput(*out, buildLabelsMap())
}

// buildLabelsMap is the pure-data half of runLabels — exposed for the
// unit test to assert shape without going through the CLI.
func buildLabelsMap() map[string]labelEntry {
	types := unionDiscoverTypes()
	m := make(map[string]labelEntry, len(types))
	for _, t := range types {
		m[t] = labelEntry{
			Label:   labels.Label(t),
			IconKey: labels.IconKey(t),
		}
	}
	return m
}

// unionDiscoverTypes returns the deduped, sorted union of every
// Terraform type known to the InsideOut pipeline — discoverable + codegen-
// only — across AWS and GCP. Shared helper for every subcommand that
// iterates "every TF type the pipeline knows about" (labels, capabilities
// matrix, supported-resources doc).
//
// Was registry.SupportedDiscoverTypes(AWS|GCP) pre-#494 — the rename to
// registry.KnownTypes is the bundle-12 fix: types that have Layer-1 +
// curated policy authored but no live discoverer (awsCodegenOnlyTypes)
// now appear in the capabilities matrix, so their drift-coverage credit
// is no longer silently dropped. Their Discoverable + Enrichable axes
// show ✗; DriftDetectable + AgentEditable show ✓ — correctly surfacing
// the "policy authored, discoverer pending" gap.
func unionDiscoverTypes() []string {
	return registry.KnownTypes()
}

// writeJSONOutput marshals v as deterministic indented JSON and writes
// it to outPath (stdout when empty). Shared by every JSON-emitting
// subcommand in this group for byte-for-byte parity across runs.
//
// json.Marshal sorts map keys deterministically when the map's keys are
// strings, so callers can pass map[string]any directly without a
// per-call sort pass.
func writeJSONOutput(outPath string, v any) int {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal json: %v\n", err)
		return 1
	}
	// Final newline matches `jq` / golden-file convention.
	buf = append(buf, '\n')

	if outPath == "" {
		if _, err := os.Stdout.Write(buf); err != nil {
			fmt.Fprintf(os.Stderr, "write stdout: %v\n", err)
			return 1
		}
		return 0
	}
	if err := os.WriteFile(outPath, buf, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		return 1
	}
	return 0
}
