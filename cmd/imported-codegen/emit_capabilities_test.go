package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRunCapabilities_EmitsValidJSON pins that the capabilities
// subcommand writes JSON that round-trips through Unmarshal into the
// expected map[string]capabilityRow shape. Catches accidental shape
// drift at unit-test time.
func TestRunCapabilities_EmitsValidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "capabilities.json")

	if code := runCapabilities([]string{"--output", out}); code != 0 {
		t.Fatalf("runCapabilities exit code = %d, want 0", code)
	}

	buf, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got map[string]capabilityRow
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, buf)
	}
	if len(got) == 0 {
		t.Fatal("emitted capabilities map is empty — registry returned nothing")
	}
}

// TestRunCapabilities_DeterministicOrdering pins golden-file
// stability: back-to-back runs must produce byte-identical output.
func TestRunCapabilities_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")

	if code := runCapabilities([]string{"--output", a}); code != 0 {
		t.Fatalf("first run exit code = %d, want 0", code)
	}
	if code := runCapabilities([]string{"--output", b}); code != 0 {
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

// TestBuildCapabilitiesMap_KnownEntries pins fixture-driven shape
// checks against the runtime registries. Loose contracts so the test
// stays stable as the per-type curation rolls out — assert
// Discoverable==true for known-registered types and
// RileyEditable==true for at least one curated AWS+GCP policy.
func TestBuildCapabilitiesMap_KnownEntries(t *testing.T) {
	t.Parallel()
	got := buildCapabilitiesMap()

	// Every type in the registry is Discoverable by construction.
	for _, tfType := range []string{
		"aws_s3_bucket",
		"aws_lambda_function",
		"google_pubsub_topic",
		"google_compute_network",
	} {
		row, ok := got[tfType]
		if !ok {
			t.Errorf("%s: missing from emitted capabilities map", tfType)
			continue
		}
		if !row.Discoverable {
			t.Errorf("%s: Discoverable = false, want true (type is in registry)", tfType)
		}
	}

	// At least one curated policy on each cloud declares ChatSafe /
	// RequiresApproval edits — pick one well-known type per cloud to
	// pin the wiring. If a future curation pass downgrades every
	// field on these types to non-Riley-editable, the test will fail
	// loud; update the fixture to a different type that still
	// carries editable fields.
	for _, tfType := range []string{
		"aws_s3_bucket",
		"google_storage_bucket",
	} {
		row, ok := got[tfType]
		if !ok {
			t.Errorf("%s: missing from emitted capabilities map", tfType)
			continue
		}
		if !row.RileyEditable {
			t.Errorf("%s: RileyEditable = false, want true (curated policy has ChatSafe / RequiresApproval entries)", tfType)
		}
	}
}

// TestBuildCapabilitiesMap_UnknownTypeIsAbsent pins the contract that
// types not in registry.SupportedDiscoverTypes don't accidentally
// appear in the matrix. Catches the regression where one of the
// registries (policy / bindings) leaks an entry whose tfType is
// outside the discover surface.
func TestBuildCapabilitiesMap_UnknownTypeIsAbsent(t *testing.T) {
	t.Parallel()
	got := buildCapabilitiesMap()
	if _, ok := got["aws_bogus_nonexistent_type"]; ok {
		t.Error("nonexistent type leaked into capabilities map")
	}
}
