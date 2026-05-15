package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderSupportedResources_FixtureMatrix pins the Markdown shape on
// a tiny synthetic fixture: 2 AWS + 1 GCP type, mixed boolean values.
// Locks the header text, summary line wording, per-cloud table headers,
// glyphs (✓ / –), and the "How to regenerate" footer in one place so a
// future renderer refactor either preserves every load-bearing piece
// or flips this test loud.
func TestRenderSupportedResources_FixtureMatrix(t *testing.T) {
	t.Parallel()
	caps := map[string]capabilityRow{
		"aws_s3_bucket": {
			Discoverable: true, Enrichable: true, DriftDetectable: false,
			MetricsAvailable: false, AgentEditable: true,
		},
		"aws_lambda_function": {
			Discoverable: true, Enrichable: false, DriftDetectable: false,
			MetricsAvailable: true, AgentEditable: false,
		},
		"google_storage_bucket": {
			Discoverable: true, Enrichable: true, DriftDetectable: true,
			MetricsAvailable: false, AgentEditable: true,
		},
	}
	got := renderSupportedResources(caps)

	// Header sanity.
	if !strings.HasPrefix(got, "# Supported Resources\n") {
		t.Errorf("output should start with the H1 header, got first 32 bytes = %q", got[:min(32, len(got))])
	}

	// Each axis name appears in the doc — locks the human-readable
	// glossary section.
	for _, axis := range []string{"Discoverable", "Enrichable", "DriftDetectable", "MetricsAvailable", "AgentEditable"} {
		if !strings.Contains(got, axis) {
			t.Errorf("output missing axis name %q", axis)
		}
	}

	// AWS section appears before GCP section. Order matters because
	// the downstream UI sorts cloud sections AWS-first.
	awsIdx := strings.Index(got, "## AWS")
	gcpIdx := strings.Index(got, "## GCP")
	if awsIdx < 0 || gcpIdx < 0 {
		t.Fatalf("missing per-cloud sections: awsIdx=%d gcpIdx=%d", awsIdx, gcpIdx)
	}
	if awsIdx > gcpIdx {
		t.Error("GCP section appears before AWS section — order is wrong")
	}

	// AWS rows must be sorted by TF type (aws_lambda_function < aws_s3_bucket).
	lambdaIdx := strings.Index(got, "`aws_lambda_function`")
	s3Idx := strings.Index(got, "`aws_s3_bucket`")
	if lambdaIdx < 0 || s3Idx < 0 {
		t.Fatalf("missing AWS rows: lambda=%d s3=%d", lambdaIdx, s3Idx)
	}
	if lambdaIdx > s3Idx {
		t.Error("AWS rows are not sorted by TF type")
	}

	// Glyphs: aws_s3_bucket has Discoverable=true, Enrichable=true,
	// DriftDetectable=false, MetricsAvailable=false, AgentEditable=true
	// → row should be "| ✓ | ✓ | – | – | ✓ |".
	wantS3Row := "| `aws_s3_bucket` | ✓ | ✓ | – | – | ✓ |"
	if !strings.Contains(got, wantS3Row) {
		t.Errorf("aws_s3_bucket row mismatch — want substring %q\n--- full doc ---\n%s", wantS3Row, got)
	}

	// Summary line — both clouds use the rounded percentages branch.
	// AWS: 2 types, 100% Discoverable, 50% Enrichable, 0% Drift, 50% Metrics, 50% Agent.
	wantAWSSummary := "**AWS:** 2 types · 100% Discoverable · 50% Enrichable · 0% DriftDetectable · 50% MetricsAvailable · 50% AgentEditable"
	if !strings.Contains(got, wantAWSSummary) {
		t.Errorf("AWS summary line mismatch — want substring %q", wantAWSSummary)
	}

	// Footer documents the regen command.
	if !strings.Contains(got, "make regen-supported-resources") {
		t.Error("footer missing the canonical regen command")
	}
	if !strings.Contains(got, "go run ./cmd/imported-codegen supported-resources --output SUPPORTED_RESOURCES.md") {
		t.Error("footer missing the direct go-run regen command")
	}
}

// TestRenderSupportedResources_DropsUnknownCloud pins that the renderer
// gracefully ignores types that don't match a known cloud prefix
// instead of routing them into one section or panicking. Future-proof
// against a third provider showing up in the discover registry.
func TestRenderSupportedResources_DropsUnknownCloud(t *testing.T) {
	t.Parallel()
	caps := map[string]capabilityRow{
		"aws_s3_bucket":           {Discoverable: true},
		"google_storage_bucket":   {Discoverable: true},
		"azurerm_storage_account": {Discoverable: true},
	}
	got := renderSupportedResources(caps)
	if strings.Contains(got, "azurerm_storage_account") {
		t.Error("unknown-prefix type should not appear in output until a cloud section is added for it")
	}
	// Counts in the summary line should not include the dropped type.
	if !strings.Contains(got, "**AWS:** 1 types") {
		t.Error("AWS summary count should be 1")
	}
	if !strings.Contains(got, "**GCP:** 1 types") {
		t.Error("GCP summary count should be 1")
	}
}

// TestRenderSupportedResources_EmptyCloud pins the empty-cloud branch:
// a cloud with zero registered types renders a "no types" placeholder
// and a zero-count summary line rather than an empty table that would
// produce broken Markdown.
func TestRenderSupportedResources_EmptyCloud(t *testing.T) {
	t.Parallel()
	caps := map[string]capabilityRow{
		"aws_s3_bucket": {Discoverable: true},
	}
	got := renderSupportedResources(caps)
	if !strings.Contains(got, "## GCP") {
		t.Fatal("GCP section header should appear even when empty")
	}
	if !strings.Contains(got, "_No resource types registered for this cloud._") {
		t.Error("empty cloud should render the no-types placeholder")
	}
	if !strings.Contains(got, "**GCP:** 0 types") {
		t.Error("empty cloud summary should report 0 types")
	}
}

// TestRunSupportedResources_WriteAndCheckRoundtrip pins the
// write→check loop a developer (or CI) runs: writing the file then
// running --check should exit zero, and tampering with the file should
// flip --check to non-zero.
func TestRunSupportedResources_WriteAndCheckRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "SUPPORTED_RESOURCES.md")

	if code := runSupportedResources([]string{"--output", out}); code != 0 {
		t.Fatalf("first write: runSupportedResources exit = %d, want 0", code)
	}

	if code := runSupportedResources([]string{"--check", "--output", out}); code != 0 {
		t.Fatalf("--check on freshly-written file: exit = %d, want 0", code)
	}

	// Tamper with the file and confirm --check trips.
	buf, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read for tamper: %v", err)
	}
	tampered := append(buf, []byte("\n<!-- accidental hand-edit -->\n")...)
	if err := os.WriteFile(out, tampered, 0o644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}
	if code := runSupportedResources([]string{"--check", "--output", out}); code == 0 {
		t.Error("--check on tampered file: exit = 0, want non-zero")
	}
}

// TestRunSupportedResources_RequiresOutput pins that the subcommand
// rejects an empty --output flag rather than silently writing nowhere.
// This is a user-experience guard — accidentally omitting --output
// would otherwise produce a confusing "wrote 0 bytes" or panic.
func TestRunSupportedResources_RequiresOutput(t *testing.T) {
	t.Parallel()
	if code := runSupportedResources([]string{}); code == 0 {
		t.Error("expected non-zero exit when --output is omitted")
	}
	if code := runSupportedResources([]string{"--output", ""}); code == 0 {
		t.Error("expected non-zero exit when --output is empty")
	}
}

// TestRunSupportedResources_CheckAgainstMissingFile pins the --check
// failure mode when the target file does not exist (e.g. a contributor
// deleted SUPPORTED_RESOURCES.md and pushed). --check should fail loud
// rather than silently regenerating a fresh copy.
func TestRunSupportedResources_CheckAgainstMissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "does-not-exist.md")
	if code := runSupportedResources([]string{"--check", "--output", out}); code == 0 {
		t.Error("expected non-zero exit when --check is run against a missing file")
	}
}

// TestSplitCloudTypes_Sorted pins the split helper's sort guarantee.
// Downstream rendering depends on it for deterministic table row order.
func TestSplitCloudTypes_Sorted(t *testing.T) {
	t.Parallel()
	caps := map[string]capabilityRow{
		"aws_zzz":   {},
		"aws_aaa":   {},
		"aws_mmm":   {},
		"google_z":  {},
		"google_a":  {},
		"unknown_x": {},
	}
	aws, gcp := splitCloudTypes(caps)
	if got, want := aws, []string{"aws_aaa", "aws_mmm", "aws_zzz"}; !equalSlices(got, want) {
		t.Errorf("aws = %v, want %v", got, want)
	}
	if got, want := gcp, []string{"google_a", "google_z"}; !equalSlices(got, want) {
		t.Errorf("gcp = %v, want %v", got, want)
	}
}

// TestPct pins the rounding contract on the per-cloud summary counts.
func TestPct(t *testing.T) {
	t.Parallel()
	cases := []struct {
		num, den, want int
	}{
		{0, 0, 0},   // empty cloud branch
		{0, 10, 0},  // nothing flagged
		{10, 10, 100},
		{1, 3, 33},   // round down
		{2, 3, 67},   // round up
		{1, 2, 50},
	}
	for _, tc := range cases {
		if got := pct(tc.num, tc.den); got != tc.want {
			t.Errorf("pct(%d,%d) = %d, want %d", tc.num, tc.den, got, tc.want)
		}
	}
}

func equalSlices(a, b []string) bool {
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
