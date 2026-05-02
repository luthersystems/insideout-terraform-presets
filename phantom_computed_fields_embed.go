package terraformpresets

import _ "embed"

// PhantomComputedFieldsTXT is the contents of phantom-computed-fields.txt
// at repo root, exposed as a package-level variable so subpackages
// (notably pkg/drift) can consume the denylist without traversing upward
// in the source tree (go:embed forbids `..` paths).
//
// The file is the canonical list of pure-Computed provider attributes
// that drift on `terraform plan -refresh-only` but cannot be silenced
// via lifecycle.ignore_changes (terraform#30517). Format and ownership
// are documented in the file itself; CI gates
// (tests/verify-phantom-computed-schema.sh,
// tests/lint-phantom-computed-fields.sh) keep it in sync with provider
// schemas and module NOTE comments. The pkg/drift classifier parses
// this content into a resource-type → attribute denylist used by the
// phantomComputedRule (see pkg/drift/rules.go).
//
//go:embed phantom-computed-fields.txt
var PhantomComputedFieldsTXT []byte
