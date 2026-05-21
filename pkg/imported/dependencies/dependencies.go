// Package dependencies is the canonical, runtime-importable registry of
// field-name → target-Terraform-type cross-references for imported
// resources. It is the single source of truth for the map that used to
// live as an unexported `crossRefMap` in `cmd/imported-codegen`'s
// `dependencies` subcommand (presets#482).
//
// Two kinds of consumer use this map:
//
//   - The imported-codegen `dependencies` subcommand, which aggregates
//     it to the per-type edge list emitted as `imported-dependencies.json`
//     (type → []type). That JSON deliberately drops the field-name level.
//
//   - Per-instance dependency matchers (e.g. reliable's import wizard),
//     which walk a concrete resource's attributes and, for each cross-ref
//     field, resolve the sibling target. The aggregated JSON cannot
//     answer that — it needs the field-name level. Before this package
//     existed, reliable hand-mirrored `crossRefMap` in
//     `internal/agentapi/import_dependencies.go` with a drift test
//     (issue #667). Such consumers now call FieldRefs / Lookup instead.
//
// Authoring rule: the map is intentionally small and conservative —
// only well-known, unambiguous cross-resource references. A false-
// positive edge propagates into the eventual UI dependency graph, which
// is worse than a missing edge; missing edges can be filled in later by
// appending an entry. Keys are the lowercase Terraform tag name exactly
// as declared on the generated struct's `tf:"<name>"`.
package dependencies

import "maps"

// fieldRefs is the curated set of (Terraform-tag-name → target-TF-type)
// pairs. Each entry says: "any *Value[string] (or []*Value[string])
// field with this tf tag, on any Layer-1 typed struct, references a
// resource of type <target>".
//
// The map is keyed by bare field name — not (tfType, field) — by
// design: the same well-known field name (e.g. `vpc_id`) carries the
// same target meaning on every resource that declares it. This keeps
// the registry tiny and is the contract per-instance consumers depend
// on.
//
// To extend: add the field-name and target TF type here. The
// imported-codegen emitter and the contract test in dependencies_test.go
// pick it up automatically; the JSON golden bumps on the next codegen
// run.
var fieldRefs = map[string]string{
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

// FieldRefs returns the canonical field-name → target Terraform type
// map used to resolve per-instance cross-references in imported
// resources. The returned map is a fresh copy on every call — callers
// may read, range, or mutate it without affecting the registry or
// other callers.
func FieldRefs() map[string]string {
	return maps.Clone(fieldRefs)
}

// Lookup returns the target Terraform type for a cross-ref field name
// and true; or ("", false) if the field is not a recognized cross-
// reference. It saves per-instance consumers a FieldRefs() copy plus
// their own nil check when resolving one field at a time.
func Lookup(field string) (string, bool) {
	target, ok := fieldRefs[field]
	return target, ok
}
