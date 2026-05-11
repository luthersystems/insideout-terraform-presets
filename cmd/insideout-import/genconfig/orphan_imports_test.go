package genconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPruneOrphanImports_DropsOrphan covers the canonical F1-class
// (#357 / #362) case: imports.tf has an import block whose target
// type+name has no matching resource block in generated.tf, so the
// import is dropped and surfaces in the returned slice.
func TestPruneOrphanImports_DropsOrphan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, importsFile), []byte(`import {
  to = aws_vpc.main
  id = "vpc-123"
}

import {
  to = aws_network_acl.default_nacl
  id = "acl-deadbeef"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := []byte(`resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`)

	skipped, err := pruneOrphanImports(dir, generated)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 {
		t.Fatalf("len(skipped)=%d, want 1", len(skipped))
	}
	got := skipped[0]
	if got.Address != "aws_network_acl.default_nacl" {
		t.Errorf("Address=%q, want aws_network_acl.default_nacl", got.Address)
	}
	if got.ImportID != "acl-deadbeef" {
		t.Errorf("ImportID=%q, want acl-deadbeef", got.ImportID)
	}
	if got.Reason != "no_generated_config" {
		t.Errorf("Reason=%q, want no_generated_config", got.Reason)
	}
	// imports.tf was rewritten — the orphan is gone, the survivor remains.
	rewritten, err := os.ReadFile(filepath.Join(dir, importsFile))
	if err != nil {
		t.Fatal(err)
	}
	rewrittenStr := string(rewritten)
	if !strings.Contains(rewrittenStr, "aws_vpc.main") {
		t.Errorf("non-orphan import must survive\n--- got ---\n%s", rewrittenStr)
	}
	if strings.Contains(rewrittenStr, "aws_network_acl.default_nacl") {
		t.Errorf("orphan import must be dropped\n--- got ---\n%s", rewrittenStr)
	}
}

// TestPruneOrphanImports_AllNonOrphanLeavesFileUntouched pins the
// byte-stability guarantee: when there are no orphans the file is
// not rewritten. This matters because genconfig's golden tests rely
// on imports.tf being a stable byte stream across runs.
func TestPruneOrphanImports_AllNonOrphanLeavesFileUntouched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	imports := []byte(`import {
  to = aws_vpc.main
  id = "vpc-123"
}
`)
	if err := os.WriteFile(filepath.Join(dir, importsFile), imports, 0o644); err != nil {
		t.Fatal(err)
	}
	generated := []byte(`resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`)
	skipped, err := pruneOrphanImports(dir, generated)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("len(skipped)=%d, want 0", len(skipped))
	}
	// Read back — must be byte-identical to input.
	got, err := os.ReadFile(filepath.Join(dir, importsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(imports) {
		t.Errorf("imports.tf must be byte-identical when no orphans\n--- want ---\n%s\n--- got ---\n%s", imports, got)
	}
}

// TestPruneOrphanImports_ReturnsEmptySliceNotNil pins the JSON shape
// contract documented in OrphanImport. Empty result must be the
// empty slice so the JSON wrapper marshals as `{"imports":[]}` not
// `{"imports":null}` — same contract as #255 inspector returns.
//
// Exercises the non-trivial early-return path: a non-empty imports.tf
// with all imports having matching resource bodies. A mutation that
// changed the `skipped := []OrphanImport{}` declaration at the top of
// pruneOrphanImports to `var skipped []OrphanImport` would make the
// early return at len==0 yield a nil slice (still len==0 but nil)
// and break the JSON contract. The empty-input version of this test
// would not catch that — both branches of the regression produce
// length 0 — but a non-empty-input version forces the code through
// the loop AND the early return, pinning the slice declaration.
func TestPruneOrphanImports_ReturnsEmptySliceNotNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, importsFile), []byte(`import {
  to = aws_vpc.main
  id = "vpc-123"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	generated := []byte(`resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`)
	skipped, err := pruneOrphanImports(dir, generated)
	if err != nil {
		t.Fatal(err)
	}
	if skipped == nil {
		t.Fatal("skipped is nil; want empty slice (the JSON wrapper relies on non-nil for `[]` not `null`)")
	}
	if len(skipped) != 0 {
		t.Fatalf("len(skipped)=%d, want 0", len(skipped))
	}
}

// TestPruneOrphanImports_OrphanWithMissingIDEmitsEmptyImportID pins
// the production-code contract that an orphan import block with no
// `id = "..."` attribute is dropped but emits ImportID="". A
// regression making stringLitFromAttr panic on a nil attribute would
// be caught here — the orphan import has a `to` (so it gets
// classified as orphan) but no `id` (so stringLitFromAttr returns "").
func TestPruneOrphanImports_OrphanWithMissingIDEmitsEmptyImportID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, importsFile), []byte(`import {
  to = aws_network_acl.default_nacl
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	skipped, err := pruneOrphanImports(dir, []byte(``))
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 1 {
		t.Fatalf("len(skipped)=%d, want 1", len(skipped))
	}
	if skipped[0].Address != "aws_network_acl.default_nacl" {
		t.Errorf("Address=%q", skipped[0].Address)
	}
	if skipped[0].ImportID != "" {
		t.Errorf("ImportID=%q, want \"\" (missing id attribute)", skipped[0].ImportID)
	}
	if skipped[0].Reason != "no_generated_config" {
		t.Errorf("Reason=%q", skipped[0].Reason)
	}
}

// TestPruneOrphanImports_MultipleOrphansSortedDeterministically pins
// the byte-stable wire shape of imports-skipped.json — the slice is
// sorted by (Address, ImportID) so consumers can diff successive runs.
//
// Runs the same orphan set twice with input ordering reversed,
// verifying the output is identical. This proves determinism, not
// just one example of correct sorting.
func TestPruneOrphanImports_MultipleOrphansSortedDeterministically(t *testing.T) {
	t.Parallel()
	// Sort key: (Address, ImportID). aws_a_resource.first/a-1 sorts
	// before aws_a_resource.first/a-2 sorts before aws_z_resource.last.
	wants := []struct{ addr, id string }{
		{"aws_a_resource.first", "a-1"},
		{"aws_a_resource.first", "a-2"},
		{"aws_z_resource.last", "z-1"},
	}
	cases := []struct {
		name    string
		imports string
	}{
		{
			name: "unsorted_z_first",
			imports: `import {
  to = aws_z_resource.last
  id = "z-1"
}

import {
  to = aws_a_resource.first
  id = "a-2"
}

import {
  to = aws_a_resource.first
  id = "a-1"
}
`,
		},
		{
			name: "reverse_unsorted_a_first",
			imports: `import {
  to = aws_a_resource.first
  id = "a-1"
}

import {
  to = aws_a_resource.first
  id = "a-2"
}

import {
  to = aws_z_resource.last
  id = "z-1"
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, importsFile), []byte(tc.imports), 0o644); err != nil {
				t.Fatal(err)
			}
			skipped, err := pruneOrphanImports(dir, []byte(``))
			if err != nil {
				t.Fatal(err)
			}
			if len(skipped) != len(wants) {
				t.Fatalf("len(skipped)=%d, want %d", len(skipped), len(wants))
			}
			for i, w := range wants {
				if skipped[i].Address != w.addr || skipped[i].ImportID != w.id {
					t.Errorf("skipped[%d]=(%q, %q), want (%q, %q)",
						i, skipped[i].Address, skipped[i].ImportID, w.addr, w.id)
				}
			}
		})
	}
}

// TestPruneOrphanImports_MalformedToAttrLeftAlone pins the
// defensive-by-leave behavior: an import block with a non-traversal
// `to = ...` expression is too weird for this pass to reason about,
// so it's left in place for Stage 2c1 to complain about. The safety
// net is a stopgap, not a HCL-recovery tool.
func TestPruneOrphanImports_MalformedToAttrLeftAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := []byte(`import {
  to = some.deeply.nested.thing.that.is.not.a.simple.type.name
  id = "weird"
}
`)
	if err := os.WriteFile(filepath.Join(dir, importsFile), in, 0o644); err != nil {
		t.Fatal(err)
	}
	skipped, err := pruneOrphanImports(dir, []byte(``))
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("len(skipped)=%d, want 0 (malformed `to` must be left in place)", len(skipped))
	}
	got, err := os.ReadFile(filepath.Join(dir, importsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "some.deeply.nested") {
		t.Errorf("malformed import must be preserved\n--- got ---\n%s", got)
	}
}

// TestPruneOrphanImports_GeneratedParseFailureReturnsErr pins the
// fatal-on-parse-error path. The caller relies on errors to abort
// the pipeline; a silent pass-through on parse failure would mask
// upstream corruption.
func TestPruneOrphanImports_GeneratedParseFailureReturnsErr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, importsFile), []byte(``), 0o644); err != nil {
		t.Fatal(err)
	}
	// `<<<` is not valid HCL — the parser will return diagnostics.
	_, err := pruneOrphanImports(dir, []byte(`<<<not valid hcl>>>`))
	if err == nil {
		t.Fatal("expected error on malformed generated.tf")
	}
	if !strings.Contains(err.Error(), "parse generated.tf") {
		t.Errorf("err=%v, want one mentioning parse generated.tf", err)
	}
}

// TestPruneOrphanImports_ImportsFileMissingReturnsErr pins the
// fatal-on-IO-failure path. The genconfig pipeline writes imports.tf
// before calling pruneOrphanImports, so a missing file is a
// programmer error, not a soft-fail case.
func TestPruneOrphanImports_ImportsFileMissingReturnsErr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := pruneOrphanImports(dir, []byte(``))
	if err == nil {
		t.Fatal("expected error when imports.tf does not exist")
	}
	if !strings.Contains(err.Error(), "read imports.tf") {
		t.Errorf("err=%v, want one mentioning read imports.tf", err)
	}
}

// TestWriteOrphanImportsManifest_WireShape pins the JSON wire shape:
// always a wrapper object with a non-nil Imports slice. Mirrors the
// #309 break on unsupported.json + the #255 nil-vs-empty contract.
func TestWriteOrphanImportsManifest_WireShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	skipped := []OrphanImport{
		{Address: "aws_network_acl.default_nacl", ImportID: "acl-deadbeef", Reason: "no_generated_config"},
	}
	path, err := writeOrphanImportsManifest(dir, skipped)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || filepath.Base(path) != orphanImportsFile {
		t.Errorf("path=%q, want one ending in %s", path, orphanImportsFile)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper orphanImportsWrapper
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatal(err)
	}
	if len(wrapper.Imports) != 1 {
		t.Fatalf("len(Imports)=%d, want 1", len(wrapper.Imports))
	}
	if wrapper.Imports[0].Address != "aws_network_acl.default_nacl" {
		t.Errorf("Address=%q", wrapper.Imports[0].Address)
	}
	if wrapper.Imports[0].ImportID != "acl-deadbeef" {
		t.Errorf("ImportID=%q", wrapper.Imports[0].ImportID)
	}
	if wrapper.Imports[0].Reason != "no_generated_config" {
		t.Errorf("Reason=%q", wrapper.Imports[0].Reason)
	}
}

// TestWriteOrphanImportsManifest_NilSliceEmitsEmptyArray pins the
// nil-vs-empty contract on the wrapper. JSON null would crash
// consumers that iterate over `.imports` (same shape as the #255
// inspector array contract).
func TestWriteOrphanImportsManifest_NilSliceEmitsEmptyArray(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, err := writeOrphanImportsManifest(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Must contain `"imports": []`, not `"imports": null`.
	if !strings.Contains(string(raw), `"imports": []`) {
		t.Errorf("wire shape must include `\"imports\": []` for nil input\n--- got ---\n%s", raw)
	}
}
