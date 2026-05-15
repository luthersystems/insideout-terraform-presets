// emit_dependencies.go is the `dependencies` subcommand for the
// imported-codegen pipeline (presets#482, Codegen pipeline expansion).
//
// Scope (v1): emit an edge-list mapping each Layer-1 typed resource
// to the other TF types it references via cross-resource fields. The
// initial implementation uses a hand-curated map of TF-tag → target-
// type to detect references — see crossRefMap below for the list. This
// avoids the brittleness of guessing target types from field names
// like `id` or `arn` alone (e.g. `aws_lambda_function.role` is an IAM
// role ARN; `aws_lambda_function.kms_key_arn` is a KMS key) while
// staying simple enough to extend by appending to one map.
//
// Phase 3 doesn't ship a full dependency-graph consumer — this is
// informational scaffolding. Expansion follows real consumer demand:
// when a UI consumer needs an edge that isn't in crossRefMap, add
// the field-name entry and bump the golden test.
package main

import (
	"flag"
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// crossRefMap is the hand-curated set of (Terraform-tag-name →
// target-TF-type) pairs the v1 dependency-emitter recognizes. Each
// entry says: "any *Value[string] (or []*Value[string]) field with this
// tf tag, on any Layer-1 typed struct, references a resource of type
// <target>".
//
// The map is intentionally small and conservative — only well-known,
// unambiguous cross-resource references. False-positive edges are
// worse than missing edges because they propagate into the eventual
// UI graph; missing edges can be filled in later by appending entries.
//
// To extend: add the field-name (the lowercase tf-tag, exactly as
// declared on the generated struct's `tf:"<name>"`) and the target
// TF type. The emitter handles `*Value[string]` and `[]*Value[string]`
// shapes automatically; nested-block tags are not currently scanned
// (callers usually want the top-level wiring, not nested doc fields).
var crossRefMap = map[string]string{
	// GCP — compute graph
	"network":    "google_compute_network",
	"subnetwork": "google_compute_subnetwork",
	// GCP — KMS
	"kms_key_name": "google_kms_crypto_key",
	// AWS — IAM
	"role":     "aws_iam_role",
	"role_arn": "aws_iam_role",
	// AWS — KMS
	"kms_key_arn":       "aws_kms_key",
	"kms_key_id":        "aws_kms_key",
	"kms_master_key_id": "aws_kms_key",
	// AWS — VPC primitives
	"vpc_id":    "aws_vpc",
	"subnet_id": "aws_subnet",
}

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
// fields reference per crossRefMap. Nested-block fields are skipped —
// the v1 contract is top-level wiring only.
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
		target, ok := crossRefMap[name]
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
