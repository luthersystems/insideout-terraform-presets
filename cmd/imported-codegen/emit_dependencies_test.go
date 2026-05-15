package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestRunDependencies_EmitsValidJSON pins that the dependencies
// subcommand writes JSON that round-trips through Unmarshal into the
// expected shape (map[string][]string with no nil values).
func TestRunDependencies_EmitsValidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "dependencies.json")

	if code := runDependencies([]string{"--output", out}); code != 0 {
		t.Fatalf("runDependencies exit code = %d, want 0", code)
	}

	buf, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got map[string][]string
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, buf)
	}
	if len(got) == 0 {
		t.Fatal("emitted dependencies map is empty — generated.RegisteredTypes returned nothing")
	}
	// Every entry must be a non-nil slice (possibly empty) — never null.
	// Pins the "marshal as [] not null" rule documented in
	// pkg/observability/discovery/CONTRIBUTING.md.
	for tfType, edges := range got {
		if edges == nil {
			t.Errorf("%s: edges == nil, must be empty slice []", tfType)
		}
	}
}

// TestRunDependencies_DeterministicOrdering pins golden-file
// stability for the dependencies emitter.
func TestRunDependencies_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")

	if code := runDependencies([]string{"--output", a}); code != 0 {
		t.Fatalf("first run exit code = %d, want 0", code)
	}
	if code := runDependencies([]string{"--output", b}); code != 0 {
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

// TestBuildDependenciesMap_KnownEdges pins fixture-driven shape
// checks for the v1 edges curated in crossRefMap. The Layer-1
// generated structs that carry these fields are stable across
// codegen runs, so the edges they produce are stable too. If
// crossRefMap grows, add a fixture entry here.
func TestBuildDependenciesMap_KnownEdges(t *testing.T) {
	t.Parallel()
	got := buildDependenciesMap()

	cases := []struct {
		tfType string
		want   []string
	}{
		// google_compute_address has both `network` and `subnetwork`
		// fields. The crossRefMap maps `network` → google_compute_network
		// and `subnetwork` → google_compute_subnetwork (subnetworks are a
		// distinct resource type from networks — qa-professor caught the
		// earlier draft pinning both to the same target).
		{tfType: "google_compute_address", want: []string{"google_compute_network", "google_compute_subnetwork"}},
		// aws_lambda_function has a `role` field (IAM role) and a
		// `kms_key_arn` field (KMS key).
		{tfType: "aws_lambda_function", want: []string{"aws_iam_role", "aws_kms_key"}},
	}
	for _, tc := range cases {
		gotEdges, ok := got[tc.tfType]
		if !ok {
			t.Errorf("%s: missing from emitted dependencies map", tc.tfType)
			continue
		}
		// Both sides are already sorted by construction, but copy
		// before mutating for safety.
		gotCopy := append([]string(nil), gotEdges...)
		wantCopy := append([]string(nil), tc.want...)
		sort.Strings(gotCopy)
		sort.Strings(wantCopy)
		if !stringSliceEqual(gotCopy, wantCopy) {
			t.Errorf("%s: edges = %v, want %v", tc.tfType, gotEdges, tc.want)
		}
	}
}

// TestBuildDependenciesMap_EmptySliceNotNil pins #255: types with no
// recognized refs emit []string{} (marshal to `[]`), not nil
// (marshal to `null`).
func TestBuildDependenciesMap_EmptySliceNotNil(t *testing.T) {
	t.Parallel()
	got := buildDependenciesMap()
	for tfType, edges := range got {
		if edges == nil {
			t.Errorf("%s: edges == nil, want non-nil (possibly empty) slice", tfType)
		}
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
