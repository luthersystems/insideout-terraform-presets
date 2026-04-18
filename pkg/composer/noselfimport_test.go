package composer

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoParentSelfImportInNonTestFiles guards the invariant that pkg/composer
// has zero luthersystems/* imports in non-test code. The test-only escape
// hatch in helpers_test.go is fine (Go allows a subpackage's _test.go to
// import its parent), but if that import ever leaked into a non-test file the
// package would form a compile-time import cycle when this repo is consumed
// as a Go module.
func TestNoParentSelfImportInNonTestFiles(t *testing.T) {
	const parentPath = "github.com/luthersystems/insideout-terraform-presets"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read composer dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == parentPath || strings.HasPrefix(path, parentPath+"/") {
				t.Errorf("%s imports %q — non-test files must not import the parent package (would create a compile-time cycle for consumers)", name, path)
			}
		}
	}
}
