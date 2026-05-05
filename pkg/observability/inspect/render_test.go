// Tests for the MCP doc-render helpers. The four tool descriptions
// and two service tables are pinned via golden snapshots in
// testdata/render/. Update with UPDATE_GOLDEN=1 — matches the
// existing presets pattern in pkg/composer/testdata/known_fields.golden.
//
// Why golden snapshots: the strings are hand-written prose embedded
// in MCP tool registrations, and any change is a UX-visible regression
// for the agent. A snapshot test makes the diff explicit at PR time.
//
// Behavioral assertions complement the snapshots: every registered
// service appears in the AWS / GCP service tables and the
// supported-services lines (a regression that drops a service from
// the registry would silently shrink the tables).
package inspect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func TestRenderServiceTable_AWSContainsEveryRegistryService(t *testing.T) {
	t.Parallel()
	got := RenderServiceTable("aws")
	if got == "" {
		t.Fatal("RenderServiceTable(aws) returned empty")
	}
	for svc := range observability.AWSServiceActions {
		if !strings.Contains(got, "| "+svc+" |") {
			t.Errorf("AWS service table missing service %q", svc)
		}
	}
}

func TestRenderServiceTable_GCPContainsEveryRegistryService(t *testing.T) {
	t.Parallel()
	got := RenderServiceTable("gcp")
	if got == "" {
		t.Fatal("RenderServiceTable(gcp) returned empty")
	}
	for svc := range observability.GCPServiceActions {
		if !strings.Contains(got, "| "+svc+" |") {
			t.Errorf("GCP service table missing service %q", svc)
		}
	}
}

func TestRenderServiceTable_UnknownCloudReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := RenderServiceTable("azure"); got != "" {
		t.Errorf("RenderServiceTable(azure) = %q, want empty", got)
	}
	if got := RenderServiceTable(""); got != "" {
		t.Errorf("RenderServiceTable(\"\") = %q, want empty", got)
	}
}

func TestRenderSupportedServicesLine_ContainsEveryRegistryService(t *testing.T) {
	t.Parallel()
	awsLine := RenderSupportedServicesLine("aws")
	for svc := range observability.AWSServiceActions {
		if !strings.Contains(awsLine, svc) {
			t.Errorf("AWS supported services line missing %q (line=%q)", svc, awsLine)
		}
	}
	gcpLine := RenderSupportedServicesLine("gcp")
	for svc := range observability.GCPServiceActions {
		if !strings.Contains(gcpLine, svc) {
			t.Errorf("GCP supported services line missing %q (line=%q)", svc, gcpLine)
		}
	}
}

// TestRenderServiceTable_DeterministicSorted pins the alphabetical
// ordering. A regression to map-iteration order would make tool
// descriptions churn across MCP handshakes — clients dedupe them by
// content, so non-determinism breaks dedupe.
//
// Asserts BOTH consistency (5× identical) AND alphabetical order on
// the service column (consistency-only would pass for a stable-but-
// reverse-sorted output).
func TestRenderServiceTable_DeterministicSorted(t *testing.T) {
	t.Parallel()
	first := RenderServiceTable("aws")
	for i := range 5 {
		if got := RenderServiceTable("aws"); got != first {
			t.Fatalf("RenderServiceTable(aws) non-deterministic between calls (iteration %d)", i)
		}
	}
	// Pin alphabetical order on the service column. Each row looks
	// like "| ec2 | ...". Extract the service tokens between the
	// first two pipes and assert sort.StringsAreSorted.
	var services []string
	for _, line := range strings.Split(first, "\n") {
		// Skip header rows ("| Service |", "|----|").
		if !strings.HasPrefix(line, "| ") || strings.Contains(line, "Service") || strings.HasPrefix(line, "|---") {
			continue
		}
		// Extract the second segment of "| svc | actions |".
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		services = append(services, strings.TrimSpace(parts[1]))
	}
	if !sort.StringsAreSorted(services) {
		t.Errorf("services in table not sorted: %v", services)
	}
}

// TestToolDescriptions_EmbedSupportedServicesLine pins the wiring
// between tool descriptions and the registry. Every tool description
// must contain the corresponding "Supported services:" line so a
// service added to the registry surfaces in MCP without manual edits.
func TestToolDescriptions_EmbedSupportedServicesLine(t *testing.T) {
	t.Parallel()
	awsLine := RenderSupportedServicesLine("aws")
	gcpLine := RenderSupportedServicesLine("gcp")

	cases := []struct {
		name string
		desc string
		line string
	}{
		{"awsinspect", AWSInspectToolDescription, awsLine},
		{"awsinspect_batch", AWSInspectBatchToolDescription, awsLine},
		{"gcpinspect", GCPInspectToolDescription, gcpLine},
		{"gcpinspect_batch", GCPInspectBatchToolDescription, gcpLine},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(tc.desc, strings.TrimRight(tc.line, "\n")) {
				t.Errorf("%s description missing the registry-derived supported-services line %q", tc.name, tc.line)
			}
		})
	}
}

// TestToolDescriptions_GoldenSnapshots pins the exact tool-description
// string. Drift here is a UX-visible regression for the LLM. Update
// with UPDATE_GOLDEN=1.
func TestToolDescriptions_GoldenSnapshots(t *testing.T) {
	cases := []struct {
		name string
		got  string
		path string
	}{
		{"aws_service_table", AWSInspectServiceTable, "testdata/render/aws_service_table.txt"},
		{"gcp_service_table", GCPInspectServiceTable, "testdata/render/gcp_service_table.txt"},
		{"awsinspect_description", AWSInspectToolDescription, "testdata/render/awsinspect_description.txt"},
		{"gcpinspect_description", GCPInspectToolDescription, "testdata/render/gcpinspect_description.txt"},
		{"awsinspect_batch_description", AWSInspectBatchToolDescription, "testdata/render/awsinspect_batch_description.txt"},
		{"gcpinspect_batch_description", GCPInspectBatchToolDescription, "testdata/render/gcpinspect_batch_description.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Clean(tc.path)
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, []byte(tc.got), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
				t.Logf("wrote golden %s (%d bytes)", path, len(tc.got))
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v (run UPDATE_GOLDEN=1 to seed)", path, err)
			}
			if string(want) != tc.got {
				// Print a single short line on mismatch so the diff
				// is reviewable in CI logs without dumping kilobytes.
				t.Errorf("golden mismatch at %s. Run UPDATE_GOLDEN=1 to update if intentional.\nfirst-diff: golden has %d bytes, got has %d bytes", path, len(want), len(tc.got))
			}
		})
	}
}
