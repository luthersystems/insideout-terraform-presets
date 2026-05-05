package main

import (
	"bytes"
	"encoding/json"
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
