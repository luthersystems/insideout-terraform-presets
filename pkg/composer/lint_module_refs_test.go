package composer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModuleReferenceLiteralsMatchComponentKeys is the static CI gate that
// prevents the class of bug behind issue #283: any string literal in
// production code that contains `module.<X>.` must have <X> equal to a
// ComponentKey rendered as a Terraform module block.
//
// The composer emits each `module "<X>" {}` block label from a ComponentKey
// (compose.go's `block.Name = string(k)` and emit.go's
// `body.AppendNewBlock("module", []string{m.Name})`), so any cross-module
// reference must use the same value. Hand-written `"module.…"` literals in
// the past drifted from the ComponentKey value (e.g. the bare-dir form
// `aws_cloudwatchmonitoring` vs the snake form `aws_cloudwatch_monitoring`)
// because nothing tied the two together.
//
// This test scans every production .go file under the package directory,
// extracts each string literal via go/parser (so comments are skipped), and
// fails on any `module.<X>.` whose <X> is not a known ComponentKey rendered
// as a module. Fix: either use the ComponentKey value (`"module." + string(k)`,
// preferably via ModuleRef / WireRef) or, if a new identifier really is
// needed, declare a ComponentKey constant for it first and add it to
// ModulePath.
func TestModuleReferenceLiteralsMatchComponentKeys(t *testing.T) {
	valid := map[string]struct{}{}
	for k := range ModulePath {
		valid[string(k)] = struct{}{}
	}

	re := regexp.MustCompile(`module\.([a-z][a-z0-9_]*)\.`)
	fset := token.NewFileSet()

	entries, err := os.ReadDir(".")
	require.NoError(t, err, "read pkg/composer directory")

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, perr, "parse %s", name)

		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return true
			}
			for _, m := range re.FindAllStringSubmatch(s, -1) {
				if _, ok := valid[m[1]]; !ok {
					pos := fset.Position(lit.Pos())
					t.Errorf(
						"%s: literal %q references module %q which is not a known ComponentKey rendered into a module block. "+
							"Use ModuleRef(KeyXxx) or WireRef(KeyXxx, \"output\") so ComponentKey is the single source of truth (#283). "+
							"If the identifier is intentionally new, declare a ComponentKey constant and add it to ModulePath.",
						pos, s, m[1])
				}
			}
			return true
		})
	}
}

// TestComponentKeyResolvesToExistingPreset cross-checks that every
// AllComponentKeys entry's source path (via GetPresetPath) points at a
// directory that actually exists in the repo. Catches a preset directory
// rename that didn't update PresetKeyMap — the symmetric class to #283.
// Without this gate, a dir rename produces "preset for X (path %q)
// returned no files" only at compose time, after the customer has
// selected the affected component.
func TestComponentKeyResolvesToExistingPreset(t *testing.T) {
	repoRootDir := repoRoot(t)
	for _, k := range AllComponentKeys {
		cloud := CloudFor(k)
		if cloud == "" {
			continue // non-physical keys
		}
		presetPath := GetPresetPath(cloud, k, nil)
		full := filepath.Join(repoRootDir, presetPath)
		info, err := os.Stat(full)
		if !assert.NoError(t, err,
			"ComponentKey %q resolves to GetPresetPath=%q which does not exist on disk. "+
				"Did the preset directory get renamed without updating PresetKeyMap?", k, presetPath) {
			continue
		}
		assert.True(t, info.IsDir(),
			"ComponentKey %q GetPresetPath=%q is not a directory", k, presetPath)
	}
}
