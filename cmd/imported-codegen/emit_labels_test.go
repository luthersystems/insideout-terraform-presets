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
		// Curated-override cases — these strings ship in
		// luthersystems/reliable's components/import/serviceMeta.ts and
		// are pinned by the per-cloud overrides in
		// pkg/imported/labels/overrides.go. A regression in either the
		// override registration or the emit-pipeline lookup chain
		// surfaces here. The exhaustive override list is asserted by
		// the labels package's own TestCuratedOverrides_LockReliableCopy.
		{tfType: "aws_s3_bucket", wantLabel: "Bucket (S3)", wantIconKey: "s3"},
		{tfType: "aws_dynamodb_table", wantLabel: "Table (DynamoDB)", wantIconKey: "ddb"},
		{tfType: "google_pubsub_topic", wantLabel: "Pub/Sub topic", wantIconKey: "pubsub"},
		// Default-rule case — google_compute_network is in the
		// SupportedDiscoverTypes set but the override file pins
		// "VPC network" / "vpc" against reliable's serviceMeta.ts.
		{tfType: "google_compute_network", wantLabel: "VPC network", wantIconKey: "vpc"},
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

// TestBuildLabelsMap_CarriesParentTfType pins that the emitted labels
// artifact carries the parentTfType field for child resource types and
// leaves it empty for standalone types. This is the contract reliable's
// `/import` wizard depends on (reliable#1617) — without the field in the
// generated JSON the UI has no way to fold child tiles into their
// parent.
func TestBuildLabelsMap_CarriesParentTfType(t *testing.T) {
	t.Parallel()
	got := buildLabelsMap()

	cases := []struct {
		tfType     string
		wantParent string // "" => must be a standalone type
	}{
		// Child types — parentTfType must be populated.
		{tfType: "aws_s3_bucket_versioning", wantParent: "aws_s3_bucket"},
		{tfType: "aws_s3_bucket_public_access_block", wantParent: "aws_s3_bucket"},
		{tfType: "aws_route_table", wantParent: "aws_vpc"},
		{tfType: "aws_vpc_security_group_ingress_rule", wantParent: "aws_security_group"},
		{tfType: "aws_kms_alias", wantParent: "aws_kms_key"},
		{tfType: "aws_cloudwatch_log_stream", wantParent: "aws_cloudwatch_log_group"},
		{tfType: "aws_iam_role_policy_attachment", wantParent: "aws_iam_role"},
		{tfType: "aws_db_parameter_group", wantParent: "aws_db_instance"},
		// Standalone types — parentTfType must be the zero string.
		{tfType: "aws_s3_bucket", wantParent: ""},
		{tfType: "aws_vpc", wantParent: ""},
		{tfType: "aws_dynamodb_table", wantParent: ""},
	}
	for _, tc := range cases {
		entry, ok := got[tc.tfType]
		if !ok {
			t.Errorf("%s: missing from emitted labels map", tc.tfType)
			continue
		}
		if entry.ParentTfType != tc.wantParent {
			t.Errorf("%s: ParentTfType = %q, want %q", tc.tfType, entry.ParentTfType, tc.wantParent)
		}
	}
}

// TestRunLabels_OmitsEmptyParentTfType pins that the JSON-on-disk drops
// the parentTfType key for standalone types (omitempty) but emits it for
// child types. This locks the wire shape downstream consumers parse — a
// standalone entry stays a two-key object, a child entry gains the third
// key.
func TestRunLabels_OmitsEmptyParentTfType(t *testing.T) {
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
	// Decode into raw maps so we can assert key presence/absence rather
	// than the struct's zero-value default.
	var raw map[string]map[string]any
	if err := json.Unmarshal(buf, &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	child, ok := raw["aws_s3_bucket_versioning"]
	if !ok {
		t.Fatal("aws_s3_bucket_versioning missing from emitted JSON")
	}
	if got := child["parentTfType"]; got != "aws_s3_bucket" {
		t.Errorf("aws_s3_bucket_versioning: parentTfType key = %v, want %q", got, "aws_s3_bucket")
	}

	// A child entry keeps the existing two keys plus the new one.
	if _, present := child["label"]; !present {
		t.Error("aws_s3_bucket_versioning: label key missing")
	}
	if _, present := child["iconKey"]; !present {
		t.Error("aws_s3_bucket_versioning: iconKey key missing")
	}

	standalone, ok := raw["aws_s3_bucket"]
	if !ok {
		t.Fatal("aws_s3_bucket missing from emitted JSON")
	}
	if _, present := standalone["parentTfType"]; present {
		t.Errorf("aws_s3_bucket: parentTfType key present, want omitted (omitempty)")
	}
	// omitempty must drop only parentTfType — the other two keys stay.
	if _, present := standalone["label"]; !present {
		t.Error("aws_s3_bucket: label key missing")
	}
	if _, present := standalone["iconKey"]; !present {
		t.Error("aws_s3_bucket: iconKey key missing")
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
