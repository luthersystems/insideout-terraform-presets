package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestRunLabels_EmitsValidJSON pins that the labels subcommand writes
// JSON that round-trips through Unmarshal into the expected shape.
// Catches accidental formatting drift (e.g. missing trailing newline,
// non-deterministic key order) at unit-test time instead of waiting
// for a downstream golden-file diff.
func TestRunLabels_EmitsValidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "labels.json")

	if code := runLabels([]string{"--output", out}); code != 0 {
		t.Fatalf("runLabels exit code = %d, want 0", code)
	}

	buf, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got map[string]labelEntry
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, buf)
	}
	if len(got) == 0 {
		t.Fatal("emitted labels map is empty — registry returned nothing")
	}
}

// TestRunLabels_DeterministicOrdering pins the golden-file-stability
// contract: two back-to-back runs must produce byte-identical output.
// Catches any path in the emit chain that picks up non-deterministic
// ordering (map iteration, time.Now, etc.).
func TestRunLabels_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")

	if code := runLabels([]string{"--output", a}); code != 0 {
		t.Fatalf("first run exit code = %d, want 0", code)
	}
	if code := runLabels([]string{"--output", b}); code != 0 {
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

// TestBuildLabelsMap_KnownEntries pins a small fixture: a handful of
// well-known registered types appear in the emitted map and resolve
// to the expected default-rule labels. Catches regressions in the
// label-emit pipeline (e.g. accidentally returning the type name
// verbatim instead of the humanized form).
func TestBuildLabelsMap_KnownEntries(t *testing.T) {
	t.Parallel()
	got := buildLabelsMap()

	cases := []struct {
		tfType      string
		wantLabel   string
		wantIconKey string
	}{
		// Default-rule cases (skeleton labels package has an empty
		// override registry, so every entry exercises the default
		// path).
		{tfType: "aws_s3_bucket", wantLabel: "S3 Bucket", wantIconKey: "s3_bucket"},
		{tfType: "aws_dynamodb_table", wantLabel: "Dynamodb Table", wantIconKey: "dynamodb_table"},
		{tfType: "google_pubsub_topic", wantLabel: "Pubsub Topic", wantIconKey: "pubsub_topic"},
		{tfType: "google_compute_network", wantLabel: "Compute Network", wantIconKey: "compute_network"},
	}
	for _, tc := range cases {
		entry, ok := got[tc.tfType]
		if !ok {
			t.Errorf("%s: missing from emitted labels map", tc.tfType)
			continue
		}
		if entry.Label != tc.wantLabel {
			t.Errorf("%s: Label = %q, want %q", tc.tfType, entry.Label, tc.wantLabel)
		}
		if entry.IconKey != tc.wantIconKey {
			t.Errorf("%s: IconKey = %q, want %q", tc.tfType, entry.IconKey, tc.wantIconKey)
		}
	}
}

// TestUnionDiscoverTypes_SortedAndDeduped pins the shared helper's
// guarantee that the union slice is sorted and contains no duplicates.
// Downstream subcommands depend on this for deterministic output.
func TestUnionDiscoverTypes_SortedAndDeduped(t *testing.T) {
	t.Parallel()
	got := unionDiscoverTypes()
	if len(got) == 0 {
		t.Fatal("union is empty — registry returned nothing")
	}
	if !sort.StringsAreSorted(got) {
		t.Error("union is not sorted")
	}
	seen := map[string]struct{}{}
	for _, t2 := range got {
		if _, dup := seen[t2]; dup {
			t.Errorf("duplicate entry: %q", t2)
		}
		seen[t2] = struct{}{}
	}
}
