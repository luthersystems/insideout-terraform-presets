package main

import (
	"flag"
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// driftField is one entry in the per-type drift-fields list. The JSON
// tags use lowerCamelCase to match the downstream TS consumer's wire
// shape (same convention as labelEntry / capabilityRow).
type driftField struct {
	Path     string `json:"path"`
	Semantic string `json:"semantic"`
}

// runDriftFields is the `drift-fields` subcommand: emit a per-type
// list of field paths (with their DriftSemantic axis values) that the
// curator has opted into drift detection. Mirrors the comparator's
// dispatch surface: the eventual pkg/drift/imported package iterates
// the same set of paths for each tfType when computing CompareDrift.
//
// Output is sorted by tfType, then by field path within each type, so
// the emitted JSON is byte-stable across runs.
//
// Default destination is stdout; --output <path> writes to a file.
func runDriftFields(args []string) int {
	fs := flag.NewFlagSet("drift-fields", flag.ExitOnError)
	out := fs.String("output", "", "path to write JSON to (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return writeJSONOutput(*out, buildDriftFieldsMap())
}

// buildDriftFieldsMap is the pure-data half of runDriftFields. Returns
// a map from tfType to its sorted-by-path slice of driftField entries.
// Types with no curated DriftSemantic entries are omitted entirely
// (rather than emitted with `[]`) — the comparator dispatch surface is
// "types that have at least one drift-detectable field"; an empty
// entry would be indistinguishable from "registered but uncurated"
// and would mislead downstream consumers.
func buildDriftFieldsMap() map[string][]driftField {
	registered := policy.RegisteredTypes()
	out := map[string][]driftField{}
	for _, tfType := range registered {
		m, ok := policy.Lookup(tfType)
		if !ok {
			continue
		}
		rows := collectDriftFields(m)
		if len(rows) == 0 {
			continue
		}
		out[tfType] = rows
	}
	return out
}

// collectDriftFields projects a Layer-2 policy.Map down to the subset
// of entries with a non-empty DriftSemantic axis, sorted by path.
// Empty DriftSemantic == "no drift comparison" per axes.go, so those
// entries are filtered out before sort.
func collectDriftFields(m policy.Map) []driftField {
	paths := make([]string, 0, len(m))
	for p, fp := range m {
		if fp.DriftSemantic == "" {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]driftField, 0, len(paths))
	for _, p := range paths {
		out = append(out, driftField{
			Path:     p,
			Semantic: string(m[p].DriftSemantic),
		})
	}
	return out
}
