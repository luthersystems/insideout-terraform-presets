package composer

import "fmt"

// ModuleRef returns the canonical HCL `module.<key>` prefix for a
// ComponentKey. Use this when wiring a module reference whose attribute
// is appended later (e.g. `ModuleRef(k) + ".vpc_id"`). For full
// traversals, prefer WireRef.
//
// ComponentKey is the single source of truth for the rendered identifier
// — the composer emits `module "<key>" {}` from the same value, so this
// helper guarantees the prefix matches the declared block label.
func ModuleRef(k ComponentKey) string {
	return "module." + string(k)
}

// WireRef returns the canonical HCL traversal `module.<key>.<output>`
// for a ComponentKey and an output / resource address. Use this for
// every cross-module wire instead of hand-written `"module.…"` string
// literals — drift between the rendered block label and the reference
// then becomes a compile error rather than a terraform-init failure
// (issue #283).
func WireRef(k ComponentKey, output string) string {
	return fmt.Sprintf("module.%s.%s", k, output)
}
