package cleanup

import (
	"strings"
	"testing"
)

// TestFilterImportBlocks_DroppedList pins the dropped-targets contract.
// FilterImportBlocks now returns the list of "to" addresses that were
// filtered out so the runner can surface them at Warn. Before this
// change, un-importable references were silently dropped and the
// consuming HCL still referenced them as literal ARNs — terraform
// validate passed against a stack that referenced resources outside
// its own state, and apply failed at runtime (issue #58 review).
func TestFilterImportBlocks_DroppedList(t *testing.T) {
	cases := []struct {
		name        string
		imports     string
		generated   string
		wantKept    []string
		wantDropped []string
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filtered, dropped, err := FilterImportBlocks([]byte(tc.imports), []byte(tc.generated))
			if err != nil {
				t.Fatalf("FilterImportBlocks error: %v", err)
			}
			gotKept := []string{}
			for _, addr := range tc.wantKept {
				if !strings.Contains(string(filtered), "to = "+addr) {
					t.Errorf("expected filtered output to keep %q; got\n%s", addr, filtered)
				}
				gotKept = append(gotKept, addr)
			}
			for _, addr := range tc.wantDropped {
				if strings.Contains(string(filtered), "to = "+addr) {
					t.Errorf("expected filtered output to NOT contain dropped target %q; got\n%s", addr, filtered)
				}
			}
			if len(dropped) != len(tc.wantDropped) {
				t.Fatalf("dropped len = %d, want %d (got %v)", len(dropped), len(tc.wantDropped), dropped)
			}
			for i, want := range tc.wantDropped {
				if dropped[i] != want {
					t.Errorf("dropped[%d] = %q, want %q", i, dropped[i], want)
				}
			}
		})
	}
}
