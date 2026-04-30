package cleanup

import (
	"sort"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// TestFilterImportBlocks_DroppedAndMalformed pins the three-bucket
// contract: kept imports, dropped well-formed targets, and malformed
// blocks. Pre-#58-review FilterImportBlocks silently dropped both kinds
// and the runner reported success against incomplete HCL — terraform
// validate would pass but apply would fail at runtime when the unresolved
// reference hit the provider.
//
// Test parses the filtered HCL with hclwrite (rather than substring-
// matching) so future formatting changes from hclwrite don't false-fail.
func TestFilterImportBlocks_DroppedAndMalformed(t *testing.T) {
	cases := []struct {
		name          string
		imports       string
		generated     string
		wantKept      []string
		wantDropped   []string
		wantMalformed int
	}{
		{
			name: "all imports have matching resources",
			imports: `
import {
  to = aws_sqs_queue.q
  id = "https://sqs.us-east-1.amazonaws.com/123/q"
}
`,
			generated: `
resource "aws_sqs_queue" "q" {
  name = "q"
}
`,
			wantKept:    []string{"aws_sqs_queue.q"},
			wantDropped: nil,
		},
		{
			name: "import without matching resource is dropped and reported",
			imports: `
import {
  to = aws_sqs_queue.kept
  id = "kept-id"
}
import {
  to = aws_iam_role.missing
  id = "arn:aws:iam::123:role/missing"
}
`,
			generated: `
resource "aws_sqs_queue" "kept" {
  name = "kept"
}
`,
			wantKept:    []string{"aws_sqs_queue.kept"},
			wantDropped: []string{"aws_iam_role.missing"},
		},
		{
			name: "every import dropped (worst case for silent-failure bug)",
			imports: `
import {
  to = aws_iam_role.cross_account
  id = "arn:aws:iam::999:role/cross-account"
}
`,
			generated: `
resource "aws_sqs_queue" "q" {
  name = "q"
}
`,
			wantKept:    nil,
			wantDropped: []string{"aws_iam_role.cross_account"},
		},
		{
			name: "import with missing `to` is malformed, not dropped",
			imports: `
import {
  id = "no-target"
}
import {
  to = aws_sqs_queue.kept
  id = "kept-id"
}
`,
			generated: `
resource "aws_sqs_queue" "kept" {
  name = "kept"
}
`,
			wantKept:      []string{"aws_sqs_queue.kept"},
			wantMalformed: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filtered, dropped, malformed, err := FilterImportBlocks([]byte(tc.imports), []byte(tc.generated))
			if err != nil {
				t.Fatalf("FilterImportBlocks error: %v", err)
			}

			gotKept := parseImportTargets(t, filtered)
			sortStrings(gotKept)
			sortStrings(tc.wantKept)
			if !equalStrings(gotKept, tc.wantKept) {
				t.Errorf("filtered kept = %v, want %v", gotKept, tc.wantKept)
			}

			sortStrings(dropped)
			sortStrings(tc.wantDropped)
			if !equalStrings(dropped, tc.wantDropped) {
				t.Errorf("dropped = %v, want %v", dropped, tc.wantDropped)
			}

			if len(malformed) != tc.wantMalformed {
				t.Errorf("malformed len = %d, want %d (got %v)", len(malformed), tc.wantMalformed, malformed)
			}
		})
	}
}

// parseImportTargets returns the `to` addresses from an imports.tf body,
// using hclwrite + the production extractTraversalAddress helper. The
// test deliberately reuses the same helper the production code uses so
// a regression there fails BOTH the production behavior and the test —
// not just the test's substring expectation.
func parseImportTargets(t *testing.T, src []byte) []string {
	t.Helper()
	if len(src) == 0 {
		return nil
	}
	f, diags := hclwrite.ParseConfig(src, "filtered.tf", hcl.Pos{})
	if diags.HasErrors() {
		t.Fatalf("parse filtered HCL: %v", diags)
	}
	var out []string
	for _, block := range f.Body().Blocks() {
		if block.Type() != "import" {
			continue
		}
		toAttr := block.Body().GetAttribute("to")
		if toAttr == nil {
			continue
		}
		if addr := extractTraversalAddress(toAttr.Expr().BuildTokens(nil)); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }

func equalStrings(a, b []string) bool {
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
