package terraformpresets

import (
	"archive/zip"
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
)

// TestGCPCloudFunctionsPlaceholderZip guards against regressions of issue #168.
// Cloud Functions Gen2's Node.js Buildpack resolves the entry-point file at
// the archive root; the pre-v0.6.3 placeholder shipped index.js under tmp/
// which made every default-config deploy fail Cloud Build with
// "function.js does not exist". This test asserts the placeholder is flat,
// has package.json with a "main" field, and that index.js exports the symbol
// named by var.entry_point's default so the two cannot drift independently.
func TestGCPCloudFunctionsPlaceholderZip(t *testing.T) {
	raw, err := FS.ReadFile("gcp/cloud_functions/placeholder.zip")
	if err != nil {
		t.Fatalf("read embedded zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}

	contents := map[string]string{}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "/") {
			t.Errorf("placeholder.zip entry %q is nested; buildpack resolves entry-point at archive root, keep the placeholder flat", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		contents[f.Name] = string(b)
	}

	for _, name := range []string{"index.js", "package.json"} {
		if _, ok := contents[name]; !ok {
			t.Errorf("placeholder.zip missing %q at archive root", name)
		}
	}

	vars, err := FS.ReadFile("gcp/cloud_functions/variables.tf")
	if err != nil {
		t.Fatalf("read variables.tf: %v", err)
	}
	m := regexp.MustCompile(`(?s)variable\s+"entry_point".*?default\s*=\s*"([^"]+)"`).FindStringSubmatch(string(vars))
	if len(m) != 2 {
		t.Fatal("could not locate entry_point default in variables.tf")
	}
	want := "exports." + m[1]
	if !strings.Contains(contents["index.js"], want) {
		t.Errorf("index.js does not export %q matching var.entry_point default", m[1])
	}

	if !strings.Contains(contents["package.json"], `"main"`) {
		t.Error(`package.json missing "main" field`)
	}
}
