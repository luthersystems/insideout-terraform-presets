// emit_dependencies.go is the `dependencies` subcommand for the
// imported-codegen pipeline (presets#482, Codegen pipeline expansion).
//
// Scope (v1): emit an edge-list mapping each Layer-1 typed resource
// to the other TF types it references via cross-resource fields. The
// references are detected via the curated TF-tag → target-type map in
// the pkg/imported/dependencies package (dependencies.Lookup). That
// map avoids the brittleness of guessing target types from field
// names like `id` or `arn` alone (e.g. `aws_lambda_function.role` is
// an IAM role ARN; `aws_lambda_function.kms_key_arn` is a KMS key)
// while staying simple enough to extend by appending one entry.
//
// Phase 3 doesn't ship a full dependency-graph consumer — this is
// informational scaffolding. Expansion follows real consumer demand:
// when a UI consumer needs an edge that isn't recognized, add the
// field-name entry in pkg/imported/dependencies and bump the golden.
package main

import (
	"flag"
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/dependencies"
)

// runDependencies is the `dependencies` subcommand: walk every Layer-1
// typed struct registered in pkg/composer/imported/generated and emit
// a sorted edge-list keyed by TF type. The result is a deterministic
// JSON object — empty arrays are emitted for types with no recognized
// references so consumers can distinguish "scanned but no edges" from
// "not scanned".
//
// Default destination is stdout; --output <path> writes to a file.
func runDependencies(args []string) int {
	fs := flag.NewFlagSet("dependencies", flag.ExitOnError)
	out := fs.String("output", "", "path to write JSON to (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return writeJSONOutput(*out, buildDependenciesMap())
}

// buildDependenciesMap is the pure-data half of runDependencies.
// Returns a map from tfType to a sorted, deduped list of target tf
// types it references. Types with no recognized refs emit an empty
// slice (not nil) so the JSON shape is `[]`, not `null` — matches the
// "every slice marshals to an array, never null" rule documented in
// pkg/observability/discovery/CONTRIBUTING.md / issue #255.
func buildDependenciesMap() map[string][]string {
	tfTypes := generated.RegisteredTypes()
	out := make(map[string][]string, len(tfTypes))
	for _, t := range tfTypes {
		goType, _, ok := generated.Lookup(t)
		if !ok {
			out[t] = []string{}
			continue
		}
		out[t] = inferEdges(goType)
	}
	return out
}

// inferEdges walks the top-level fields of a Layer-1 typed struct and
// returns the sorted, deduped set of target TF types its tf-tagged
// fields reference per the dependencies registry. Nested-block fields
// are skipped — the v1 contract is top-level wiring only.
//
// Recognized field shapes:
//   - *Value[string]  (scalar reference, e.g. Network)
//   - []*Value[string] (list-of-refs, e.g. Subnetworks)
//
// Other shapes (maps, nested structs, ints, bools) are skipped: none
// of them carry cross-resource refs in the curated crossRefMap.
func inferEdges(goType reflect.Type) []string {
	// generated.Lookup returns the element type; nothing to deref.
	if goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	if goType.Kind() != reflect.Struct {
		return []string{}
	}
	seen := map[string]struct{}{}
	for i := 0; i < goType.NumField(); i++ {
		f := goType.Field(i)
		tag := f.Tag.Get("tf")
		if tag == "" {
			continue
		}
		// Skip nested blocks: their tf tag carries a "block" / "blocks"
		// suffix (e.g. `tf:"timeouts,block"`). The v1 emitter only
		// scans top-level scalar / list fields.
		if strings.Contains(tag, ",block") {
			continue
		}
		name := tag
		if i := strings.Index(name, ","); i >= 0 {
			name = name[:i]
		}
		target, ok := dependencies.Lookup(name)
		if !ok {
			continue
		}
		seen[target] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
