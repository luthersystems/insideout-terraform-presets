package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestRunDriftFields_EmitsValidJSON pins that the drift-fields
// subcommand writes JSON that round-trips through Unmarshal into the
// expected map[string][]driftField shape.
func TestRunDriftFields_EmitsValidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "drift-fields.json")

	if code := runDriftFields([]string{"--output", out}); code != 0 {
		t.Fatalf("runDriftFields exit code = %d, want 0", code)
	}

	buf, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got map[string][]driftField
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, buf)
	}
	// Empty result is currently legitimate (skeleton state: no
	// policy.Map entries with non-empty DriftSemantic yet). Don't
	// fail on len==0 — just round-trip the JSON shape.
	_ = got
}

// TestRunDriftFields_DeterministicOrdering pins golden-file
// stability for the drift-fields emitter.
func TestRunDriftFields_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")

	if code := runDriftFields([]string{"--output", a}); code != 0 {
		t.Fatalf("first run exit code = %d, want 0", code)
	}
	if code := runDriftFields([]string{"--output", b}); code != 0 {
		t.Fatalf("second run exit code = %d, want 0", code)
	}

	aBuf, err := os.ReadFile(a)
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	bBuf, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if string(aBuf) != string(bBuf) {
		t.Fatal("two runs produced different output — non-deterministic ordering somewhere in the emit chain")
	}
}

// TestBuildDriftFieldsMap_PerTypeSortedByPath pins the
// within-type sort contract: each emitted []driftField is sorted by
// path. Empty maps (skeleton state) trivially satisfy this; once
// curated DriftSemantic entries land, this catches accidental insert
// order leakage.
func TestBuildDriftFieldsMap_PerTypeSortedByPath(t *testing.T) {
	t.Parallel()
	got := buildDriftFieldsMap()
	for tfType, rows := range got {
		paths := make([]string, len(rows))
		for i, r := range rows {
			paths[i] = r.Path
		}
		if !sort.StringsAreSorted(paths) {
			t.Errorf("%s: paths not sorted: %v", tfType, paths)
		}
	}
}

// TestBuildDriftFieldsMap_NoEmptySemanticEntries pins the contract
// that empty-DriftSemantic entries are filtered out — every emitted
// row carries a non-empty semantic string. Catches a regression where
// the filter is bypassed and the "no drift comparison" sentinel leaks
// into the output.
func TestBuildDriftFieldsMap_NoEmptySemanticEntries(t *testing.T) {
	t.Parallel()
	got := buildDriftFieldsMap()
	for tfType, rows := range got {
		for _, r := range rows {
			if r.Semantic == "" {
				t.Errorf("%s: path %q has empty DriftSemantic (must be filtered out)", tfType, r.Path)
			}
		}
	}
}

// TestRunDriftFields_GoldenFile pins the byte-for-byte contents of the
// emitted drift-fields JSON against testdata/drift_fields/drift-fields.json.
//
// The golden is the canonical record of which curated fields participate
// in drift detection — the downstream comparator + reliable UI both key
// off this set. Adding or removing a DriftSemantic axis on any curated
// FieldPolicy entry will fail this test; the right response is to
// regenerate the golden and re-review:
//
//	go run ./cmd/imported-codegen drift-fields \
//	    --output cmd/imported-codegen/testdata/drift_fields/drift-fields.json
//
// Bundle D1 (#491) seeded the golden with the first 5 curated types;
// each follow-up bundle (D2..D9) will append entries and refresh the
// golden via the command above.
func TestRunDriftFields_GoldenFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "drift-fields.json")

	if code := runDriftFields([]string{"--output", out}); code != 0 {
		t.Fatalf("runDriftFields exit code = %d, want 0", code)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	goldenPath := filepath.Join("testdata", "drift_fields", "drift-fields.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v — refresh via the doc-comment command above", goldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("emitted drift-fields.json drifts from %s — refresh the golden or fix the regression", goldenPath)
	}
}
