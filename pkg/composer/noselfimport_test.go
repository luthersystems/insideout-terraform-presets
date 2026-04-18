package composer

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoForeignLutherImportsInNonTestFiles guards the invariant that
// pkg/composer's non-test code has zero luthersystems/* imports *other
// than* its own parent package root (which is how composer discovers the
// bundled preset FS). Any foreign luthersystems import — including any
// subpackage of insideout-terraform-presets other than the root — would
// couple composer to downstream consumers and block extraction. Only
// the exact parent path is permitted; subpackage imports must fail so
// that e.g. an "internal/" helper cannot accidentally leak in.
func TestNoForeignLutherImportsInNonTestFiles(t *testing.T) {
	const (
		parentPkg    = "github.com/luthersystems/insideout-terraform-presets"
		lutherPrefix = "github.com/luthersystems/"
	)

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
			if !strings.HasPrefix(path, lutherPrefix) {
				continue
			}
			if path == parentPkg {
				continue // parent package is the permitted exception
			}
			t.Errorf("%s imports %q — pkg/composer non-test code may not import luthersystems/* other than the parent package %q", name, path, parentPkg)
		}
	}
}
