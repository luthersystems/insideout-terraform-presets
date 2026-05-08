package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func validIR(t, addr, importID string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     t,
			Address:  addr,
			ImportID: importID,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
}

func TestWriteManifest_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resources := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.b", "https://example/b"),
		validIR("aws_sqs_queue", "aws_sqs_queue.a", "https://example/a"),
	}
	path, n, err := writeManifest(dir, "aws", resources)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count=%d, want 2", n)
	}
	if filepath.Base(path) != "imported.json" {
		t.Errorf("path=%q, want ends in imported.json", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []imported.ImportedResource
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decoded len=%d, want 2", len(got))
	}
	if got[0].Identity.Address != "aws_sqs_queue.a" {
		t.Errorf("first address=%q, want sorted (aws_sqs_queue.a)", got[0].Identity.Address)
	}
}

func TestWriteManifest_DeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	// Use four resources of the same type with non-alphabetic input
	// order so the sort sees the name suffix as the tiebreaker. A
	// mutation that drops the sort produces visible drift between runs.
	resources := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.delta", "d"),
		validIR("aws_sqs_queue", "aws_sqs_queue.alpha", "a"),
		validIR("aws_sqs_queue", "aws_sqs_queue.charlie", "c"),
		validIR("aws_sqs_queue", "aws_sqs_queue.bravo", "b"),
	}
	dir1, dir2 := t.TempDir(), t.TempDir()
	if _, _, err := writeManifest(dir1, "aws", resources); err != nil {
		t.Fatal(err)
	}
	// Reverse the input order for the second write.
	rev := make([]imported.ImportedResource, len(resources))
	for i := range resources {
		rev[len(resources)-1-i] = resources[i]
	}
	if _, _, err := writeManifest(dir2, "aws", rev); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(dir1, "imported.json"))
	b, _ := os.ReadFile(filepath.Join(dir2, "imported.json"))
	if string(a) != string(b) {
		t.Errorf("manifest output depends on input order; must be deterministic\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	// Pin the ordering: alpha < bravo < charlie < delta in the on-disk
	// file. Without this, a mutation that emits in a stable-but-wrong
	// order (e.g. by ImportID) would still produce identical bytes
	// across the two runs and slip past the cardinality test.
	got := string(a)
	if i, j := strings.Index(got, "alpha"), strings.Index(got, "bravo"); i < 0 || j < 0 || i > j {
		t.Errorf("expected alpha before bravo in sorted manifest; got positions %d,%d", i, j)
	}
	if i, j := strings.Index(got, "bravo"), strings.Index(got, "charlie"); i < 0 || j < 0 || i > j {
		t.Errorf("expected bravo before charlie; got positions %d,%d", i, j)
	}
	if i, j := strings.Index(got, "charlie"), strings.Index(got, "delta"); i < 0 || j < 0 || i > j {
		t.Errorf("expected charlie before delta; got positions %d,%d", i, j)
	}
}

func TestWriteManifest_ValidatorFailureNoFileWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := []imported.ImportedResource{
		// Missing ImportID — validator catches it.
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.bad"},
			Tier:     imported.TierImportedFlat,
		},
	}
	_, _, err := writeManifest(dir, "aws", bad)
	if err == nil {
		t.Fatal("expected validator error")
	}
	if !strings.Contains(err.Error(), "imported_resource_missing_import_id") {
		t.Errorf("expected error to mention missing-import-id code; got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(statErr) {
		t.Errorf("imported.json must NOT be written when validation fails; stat err=%v", statErr)
	}
}

func TestWriteManifest_AddressCollisionDetectedByValidator(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dup := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.same", "id-a"),
		validIR("aws_sqs_queue", "aws_sqs_queue.same", "id-b"),
	}
	_, _, err := writeManifest(dir, "aws", dup)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !strings.Contains(err.Error(), "imported_resource_address_collision") {
		t.Errorf("expected address-collision code; got: %v", err)
	}
}

// readManifest is the inverse of writeManifest: writes-then-reads round
// trip pins the wire-shape contract end-to-end. A regression that drops
// the validator (or runs only one direction) would still pass the
// individual write/read tests but fail this one.
func TestReadManifest_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.b", "https://example/b"),
		validIR("aws_sqs_queue", "aws_sqs_queue.a", "https://example/a"),
	}
	path, _, err := writeManifest(dir, "aws", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readManifest(path, "aws")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	// writeManifest sorts by Address, so the round-trip yields the
	// sorted order regardless of input order.
	if got[0].Identity.Address != "aws_sqs_queue.a" || got[1].Identity.Address != "aws_sqs_queue.b" {
		t.Errorf("addresses=%q,%q want a,b (sort preserved through round-trip)",
			got[0].Identity.Address, got[1].Identity.Address)
	}
}

// TestReadManifest_MalformedJSONIncludesOffset pins that a syntactically
// invalid manifest surfaces a json.SyntaxError offset in the error message.
// Operators editing the file by hand need a position pointer; without it
// the only recourse is bisection.
func TestReadManifest_MalformedJSONIncludesOffset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "imported.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readManifest(path, "aws")
	if err == nil {
		t.Fatal("expected decode error")
	}
	// Either "offset" or "position" — the assertion is loose to permit a
	// future swap to wrap a different error type as long as the position
	// pointer survives. Asserting an offset substring keeps the contract
	// human-debuggable without pinning the literal phrasing.
	msg := err.Error()
	if !strings.Contains(msg, "offset") && !strings.Contains(msg, "position") {
		t.Errorf("error must include byte-offset/position hint; got: %v", err)
	}
}

func TestReadManifest_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := readManifest(filepath.Join(dir, "does-not-exist.json"), "aws")
	if err == nil {
		t.Fatal("expected ENOENT-shaped error")
	}
	// Either errors.Is(err, os.ErrNotExist) (preferred — preserves the
	// sentinel chain when readManifest wraps) or the substring fallback
	// for a future refactor that wraps with %v rather than %w.
	if !os.IsNotExist(err) && !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error must wrap ErrNotExist or mention missing file; got: %v", err)
	}
}

// TestReadManifest_NullTopLevelRejected pins the writeManifest invariant:
// an empty manifest is `[]`, never `null`. Decoding `null` into a slice
// succeeds with a nil slice (validator returns nil → silent pass), so the
// reader must reject `null` explicitly.
func TestReadManifest_NullTopLevelRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "imported.json")
	if err := os.WriteFile(path, []byte("null\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readManifest(path, "aws")
	if err == nil {
		t.Fatal("expected null-top-level error; readManifest must not silently treat null as empty")
	}
	if !strings.Contains(err.Error(), "null") {
		t.Errorf("error must reference the null contract; got: %v", err)
	}
}

// TestReadManifest_ValidatorFailureSurfacesIssueCode pins that
// readManifest reuses composer.ValidateImportedResources — the same
// gate writeManifest applies — so a tampered manifest can't slip past.
// The issue-code substring keeps the error operator-actionable.
func TestReadManifest_ValidatorFailureSurfacesIssueCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := []imported.ImportedResource{
		// Missing ImportID — same shape as
		// TestWriteManifest_ValidatorFailureNoFileWritten but read-side.
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.bad"},
			Tier:     imported.TierImportedFlat,
			Source:   imported.SourceImporter,
		},
	}
	body, err := json.MarshalIndent(bad, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	path := filepath.Join(dir, "imported.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, rerr := readManifest(path, "aws")
	if rerr == nil {
		t.Fatal("expected validator error")
	}
	if !strings.Contains(rerr.Error(), "imported_resource_missing_import_id") {
		t.Errorf("expected error to mention missing-import-id code; got: %v", rerr)
	}
}

func TestWriteManifest_EmptyInputWritesJSONArrayNotNull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, n, err := writeManifest(dir, "aws", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count=%d, want 0", n)
	}
	body, _ := os.ReadFile(path)
	// json.MarshalIndent of a nil slice writes "null", which downstream
	// consumers (Reliable, Riley) cannot range over. Pin that the writer
	// emits an empty JSON array even on empty input.
	if strings.TrimSpace(string(body)) == "null" {
		t.Errorf("manifest must be `[]` not `null` for empty input; got: %s", body)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		t.Errorf("manifest must start with `[`; got: %s", body)
	}
	if !bytes.HasSuffix(body, []byte("\n")) {
		t.Errorf("manifest must end with a trailing newline; got: %q", body)
	}
}
