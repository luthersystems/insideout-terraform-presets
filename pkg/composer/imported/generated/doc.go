// Package generated holds the Layer 1 typed model for imported Terraform
// resources. The package contains both:
//
//   - Hand-written runtime primitives that the generator and consumers depend
//     on: Value[T] (the absent/null/literal/expression wrapper), FieldSchema
//     (per-attribute provider metadata), the type registry, and the
//     reflection-driven HCL marshaler/unmarshaler.
//   - Code-generated structs (one per supported Terraform resource type),
//     emitted by cmd/imported-codegen from a filtered provider schema dump.
//     These files end in *.gen.go and must not be hand-edited.
//
// The full design lives in docs/managed-resource-tiers.md, especially the
// "Layer 1 — generated, full-fidelity" section.
//
// Importing this package registers every supported resource type via the
// init() side effects in the generated files. Consumers (composer, validators,
// Riley's edit path) discover types through the registry rather than naming
// the structs directly, so adding a type does not require touching call
// sites.
package generated
