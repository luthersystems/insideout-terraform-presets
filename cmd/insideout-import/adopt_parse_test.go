package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestParseAddresses_HappyPath asserts that a small HCL fragment with
// three resources of different types yields the expected addresses,
// types, names, and 1-based line numbers. The fixture is intentionally
// small enough to inline so the line-number assertions stay obvious.
func TestParseAddresses_HappyPath(t *testing.T) {
	src := `resource "aws_sqs_queue" "dlq" {
  name = "dlq"
}

resource "aws_s3_bucket" "logs" {
  bucket = "logs"
}

resource "aws_iam_role" "task" {
  name = "task"
}
`
	got, err := ParseAddresses([]byte(src), "happy.tf")
	if err != nil {
		t.Fatalf("ParseAddresses returned unexpected error: %v", err)
	}
	want := []AdoptAddressEntry{
		{Address: "aws_sqs_queue.dlq", Type: "aws_sqs_queue", Name: "dlq", File: "happy.tf", Line: 1},
		{Address: "aws_s3_bucket.logs", Type: "aws_s3_bucket", Name: "logs", File: "happy.tf", Line: 5},
		{Address: "aws_iam_role.task", Type: "aws_iam_role", Name: "task", File: "happy.tf", Line: 9},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestParseAddresses_HCLSyntaxErrorReturnedAsError asserts that a
// malformed HCL document surfaces as a non-nil error whose string carries
// the filename plus line/column context (so callers can render
// "filename:line:col: ..." messages back to operators).
func TestParseAddresses_HCLSyntaxErrorReturnedAsError(t *testing.T) {
	// Unbalanced brace — guarantees a parse-time diagnostic.
	src := `resource "aws_sqs_queue" "dlq" {
  name = "dlq"
`
	_, err := ParseAddresses([]byte(src), "broken.tf")
	if err == nil {
		t.Fatalf("ParseAddresses returned nil error for malformed HCL")
	}
	msg := err.Error()
	if !strings.Contains(msg, "broken.tf") {
		t.Errorf("error message %q does not contain filename %q", msg, "broken.tf")
	}
	// Standard hcl diagnostic format: filename:line:col: <text>.
	// Asserting only on a literal ":" is tautological — every Go error
	// string contains a colon. This regex pins the exact shape (the
	// filename followed by two numeric segments) so a regression that
	// dropped the diagnostic position fails here.
	if !regexp.MustCompile(`broken\.tf:\d+:\d+:`).MatchString(msg) {
		t.Errorf("error message %q does not match `broken.tf:LINE:COL:` shape", msg)
	}
	if !strings.Contains(msg, "adopt.ParseAddresses") {
		t.Errorf("error message %q is missing adopt.ParseAddresses prefix", msg)
	}
}

// TestParseAddresses_IgnoresNonResourceBlocks asserts that every
// non-`resource` top-level block type is skipped and only the resource
// blocks come back. Each block type that adopt's v1 explicitly skips
// (per the function header) is exercised here so a future refactor that
// accidentally starts emitting `data` / `module` addresses fails this
// test instead of leaking through.
func TestParseAddresses_IgnoresNonResourceBlocks(t *testing.T) {
	src := `terraform {
  required_version = ">= 1.5"
}

provider "aws" {
  region = "us-east-1"
}

variable "name" {
  type = string
}

output "queue" {
  value = "x"
}

locals {
  prefix = "p"
}

data "aws_caller_identity" "me" {}

module "child" {
  source = "./child"
}

resource "aws_sqs_queue" "kept" {
  name = "kept"
}
`
	got, err := ParseAddresses([]byte(src), "mixed.tf")
	if err != nil {
		t.Fatalf("ParseAddresses returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 resource block kept, got %d: %+v", len(got), got)
	}
	if got[0].Address != "aws_sqs_queue.kept" {
		t.Errorf("kept resource address = %q, want %q", got[0].Address, "aws_sqs_queue.kept")
	}
	if got[0].Type != "aws_sqs_queue" || got[0].Name != "kept" {
		t.Errorf("kept resource type/name = %q/%q, want aws_sqs_queue/kept", got[0].Type, got[0].Name)
	}
}

// TestParseAddresses_PreservesSourceOrder asserts that the returned
// slice matches file declaration order even when block types are
// scrambled. HCL preserves Body.Blocks order; this test guards against a
// future refactor that sorts internally and breaks consumers relying on
// "first-in-file = first-emitted".
func TestParseAddresses_PreservesSourceOrder(t *testing.T) {
	src := `resource "aws_iam_role" "r3" {}
resource "aws_sqs_queue" "r1" {}
resource "aws_s3_bucket" "r5" {}
resource "aws_iam_role" "r2" {}
resource "aws_sqs_queue" "r4" {}
`
	got, err := ParseAddresses([]byte(src), "ordered.tf")
	if err != nil {
		t.Fatalf("ParseAddresses returned unexpected error: %v", err)
	}
	wantNames := []string{"r3", "r1", "r5", "r2", "r4"}
	if len(got) != len(wantNames) {
		t.Fatalf("got %d entries, want %d", len(got), len(wantNames))
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Errorf("entry[%d].Name = %q, want %q (order broken)", i, got[i].Name, want)
		}
	}
}

// TestParseAddresses_EmptyInputReturnsEmptySlice asserts that the empty
// HCL document returns a non-nil empty slice. The non-nil contract
// matters because the discovery-inspector convention (#255) requires
// JSON-marshaled output to be `[]`, never `null`, so reliable's UI
// renders an empty-state row rather than the deploy-first fallback.
func TestParseAddresses_EmptyInputReturnsEmptySlice(t *testing.T) {
	got, err := ParseAddresses([]byte(""), "empty.tf")
	if err != nil {
		t.Fatalf("ParseAddresses returned unexpected error: %v", err)
	}
	if got == nil {
		t.Fatalf("ParseAddresses returned nil slice; want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("ParseAddresses returned %d entries; want 0", len(got))
	}
	// Belt-and-suspenders: assert the JSON shape too.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(empty result) returned unexpected error: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("json.Marshal = %q, want %q", string(b), "[]")
	}
}

// TestParseAddresses_FilenameThreadsToError asserts that the filename
// argument propagates into the error string for a malformed document.
// This is the contract reliable's wizard relies on to render
// "filename:line: ..." in the upload-hcl error toast.
func TestParseAddresses_FilenameThreadsToError(t *testing.T) {
	src := `resource "aws_sqs_queue" {
}
`
	_, err := ParseAddresses([]byte(src), "my.tf")
	if err == nil {
		t.Fatalf("ParseAddresses returned nil error for missing label")
	}
	if !strings.Contains(err.Error(), "my.tf") {
		t.Errorf("error %q missing filename %q", err.Error(), "my.tf")
	}
}

// TestParseAddresses_ModuleQualifiedAddressInComment is a pin: the HCL
// parser does not auto-recurse into a referenced child `module` source
// directory, so we never see `module.x.aws_sqs_queue.dlq` from a single
// ParseAddresses call. v1 of the helper reflects this: only root-scoped
// blocks come back, with ModulePath always empty.
func TestParseAddresses_ModuleQualifiedAddressInComment(t *testing.T) {
	src := `module "child" {
  source = "./child"
}

resource "aws_sqs_queue" "root_q" {}
`
	got, err := ParseAddresses([]byte(src), "parent.tf")
	if err != nil {
		t.Fatalf("ParseAddresses returned unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 root resource, got %d: %+v", len(got), got)
	}
	if got[0].ModulePath != "" {
		t.Errorf("ModulePath = %q, want empty (v1 does not recurse modules)", got[0].ModulePath)
	}
	if got[0].Address != "aws_sqs_queue.root_q" {
		t.Errorf("Address = %q, want aws_sqs_queue.root_q", got[0].Address)
	}
}

// TestParseAddresses_QuotedLabelsAccepted pins the shape adopt expects
// from upstream: HCL resource labels are quoted strings. HCL's grammar
// does not actually permit bare-identifier labels for resource blocks
// (the `LabelNames []string` slot in resource block definitions is
// always quoted in Terraform), so this test asserts the quoted form
// works and documents the lack of a bare-identifier alternative.
func TestParseAddresses_QuotedLabelsAccepted(t *testing.T) {
	src := `resource "aws_sqs_queue" "dlq" {}
resource "aws_sqs_queue" "main_q" {}
`
	got, err := ParseAddresses([]byte(src), "labels.tf")
	if err != nil {
		t.Fatalf("ParseAddresses returned unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	want := []string{"aws_sqs_queue.dlq", "aws_sqs_queue.main_q"}
	for i, w := range want {
		if got[i].Address != w {
			t.Errorf("entry[%d].Address = %q, want %q", i, got[i].Address, w)
		}
	}
}
