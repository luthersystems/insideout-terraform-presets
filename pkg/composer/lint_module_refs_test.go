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

// modRefRe matches `module.<X>.` traversals inside a string literal.
var modRefRe = regexp.MustCompile(`module\.([a-z][a-z0-9_]*)\.`)

// fmtSprintfModuleRe matches `module.%s` (or `module.%[N]s`) in a format
// string — the most natural way a developer might recompose a wire
// reference dynamically and silently bypass the static lint's literal
// scan. We flag any such format and force the helper.
var fmtSprintfModuleRe = regexp.MustCompile(`module\.%(\[\d+\])?s\.?`)

// concatStringLiteralValue walks a Go expression and, if it is a chain
// of `+`-concatenated BasicLit string literals, returns the joined
// string and true. This unwraps the static-lint evasion case
// `"module." + "<key>" + ".<output>"` (issue #283 P0-1) so the
// reference regex can still see the full traversal. Returns "", false
// for any expression that isn't a pure literal-only concatenation
// (mixed identifiers, function calls, etc.) — those are handled
// separately by the fmt.Sprintf detector.
func concatStringLiteralValue(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, lok := concatStringLiteralValue(x.X)
		r, rok := concatStringLiteralValue(x.Y)
		if !lok || !rok {
			return "", false
		}
		return l + r, true
	case *ast.ParenExpr:
		return concatStringLiteralValue(x.X)
	}
	return "", false
}

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
// Three scan modes (each catches an evasion that the previous mode misses):
//
//  1. Bare BasicLit string literals — the simplest case.
//  2. Concatenation chains of BasicLit literals (`"module." + "X" + ".y"`)
//     — go/parser emits each segment as a separate node, so the bare-lit
//     scan misses them. Reassembled via concatStringLiteralValue before
//     applying the regex.
//  3. fmt.Sprintf calls whose format string contains `module.%s.…` — a
//     dynamic-substitution form is structurally indistinguishable from
//     the helper at compile time, so we force the helper to be used
//     instead. The lint flags any such Sprintf as a hint to call
//     ModuleRef / WireRef.
//
// Fix paths when the lint trips: either use the ComponentKey value via
// `ModuleRef(KeyXxx)` / `WireRef(KeyXxx, "output")` (the canonical
// path), or — if a new identifier really is needed — declare a
// ComponentKey constant for it first and add it to ModulePath.
func TestModuleReferenceLiteralsMatchComponentKeys(t *testing.T) {
	valid := map[string]struct{}{}
	for k := range ModulePath {
		valid[string(k)] = struct{}{}
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "read pkg/composer directory")

	checkLiteral := func(s string, pos token.Position) {
		for _, m := range modRefRe.FindAllStringSubmatch(s, -1) {
			if _, ok := valid[m[1]]; !ok {
				t.Errorf(
					"%s: literal %q references module %q which is not a known ComponentKey rendered into a module block. "+
						"Use ModuleRef(KeyXxx) or WireRef(KeyXxx, \"output\") so ComponentKey is the single source of truth (#283). "+
						"If the identifier is intentionally new, declare a ComponentKey constant and add it to ModulePath.",
					pos, s, m[1])
			}
		}
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Skip wireref.go itself: it is the canonical helper that
		// builds `module.<key>.<output>` strings via fmt.Sprintf and
		// concat — flagging its own implementation would defeat the
		// purpose. The helper is exercised through every consumer in
		// production code, so any breakage there fails real tests.
		if name == "wireref.go" {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, perr, "parse %s", name)

		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					return true
				}
				s, uerr := strconv.Unquote(x.Value)
				if uerr != nil {
					return true
				}
				checkLiteral(s, fset.Position(x.Pos()))
			case *ast.BinaryExpr:
				// Reassemble pure-literal concat chains and check the joined value.
				// We only inspect ADD nodes whose root has at least one nested ADD,
				// to avoid double-checking single-literal cases already handled above.
				if x.Op != token.ADD {
					return true
				}
				if s, ok := concatStringLiteralValue(x); ok {
					checkLiteral(s, fset.Position(x.Pos()))
				}
			case *ast.CallExpr:
				// Flag fmt.Sprintf("module.%s.…", …) — a structural
				// evasion of the literal scan. Any such use should
				// route through ModuleRef / WireRef so ComponentKey
				// is the only source of truth.
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "fmt" {
					return true
				}
				if sel.Sel.Name != "Sprintf" && sel.Sel.Name != "Errorf" && sel.Sel.Name != "Fprintf" {
					return true
				}
				if len(x.Args) == 0 {
					return true
				}
				lit, ok := x.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				fmtStr, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					return true
				}
				if fmtSprintfModuleRe.MatchString(fmtStr) {
					t.Errorf(
						"%s: fmt.%s format string %q dynamically composes a `module.<X>.…` reference. "+
							"This bypasses the literal scan and reintroduces the #283 class of bug. "+
							"Use ModuleRef(KeyXxx) or WireRef(KeyXxx, \"output\") instead so ComponentKey is the single source of truth.",
						fset.Position(lit.Pos()), sel.Sel.Name, fmtStr)
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
//
// Also exercises the polymorphic Lambda branch: GetPresetPath of
// KeyAWSEKSControlPlane with comps.AWSLambda = true returns
// `aws/lambda` instead of the default `aws/resource`, and that
// resolved path must also exist on disk.
func TestComponentKeyResolvesToExistingPreset(t *testing.T) {
	repoRootDir := repoRoot(t)

	check := func(t *testing.T, k ComponentKey, comps *Components) {
		t.Helper()
		cloud := CloudFor(k)
		if cloud == "" {
			return // non-physical key
		}
		presetPath := GetPresetPath(cloud, k, comps)
		full := filepath.Join(repoRootDir, presetPath)
		info, err := os.Stat(full)
		if !assert.NoError(t, err,
			"ComponentKey %q (comps=%v) resolves to GetPresetPath=%q which does not exist on disk. "+
				"Did the preset directory get renamed without updating PresetKeyMap?", k, comps, presetPath) {
			return
		}
		assert.True(t, info.IsDir(),
			"ComponentKey %q GetPresetPath=%q is not a directory", k, presetPath)
	}

	for _, k := range AllComponentKeys {
		check(t, k, nil)
	}

	// Polymorphic-Lambda variant: KeyAWSEKSControlPlane resolves to
	// the lambda preset when comps signals a Lambda architecture.
	enabled := true
	check(t, KeyAWSEKSControlPlane, &Components{AWSLambda: &enabled})
}
