package driftfix

import (
	"regexp"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

func TestClassifyPlan_NoOpDropped(t *testing.T) {
	t.Parallel()
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		{Address: "aws_sqs_queue.x", Change: &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionNoop}}},
	}}
	got := classifyPlan(p)
	if len(got) != 0 {
		t.Errorf("no-op resource_change must be excluded; got %v", got)
	}
}

// TestClassifyPlan_UpdateExtractsTopLevelDrift pins the contract: only
// top-level keys whose After differs from Before are reported. A naive
// implementation that returns every After key would over-patch and
// silently drop attributes that haven't drifted.
func TestClassifyPlan_UpdateExtractsTopLevelDrift(t *testing.T) {
	t.Parallel()
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change: &tfjson.Change{
			Actions: tfjson.Actions{tfjson.ActionUpdate},
			Before:  map[string]any{"name": "alpha", "delay_seconds": float64(0), "fifo_queue": false},
			After:   map[string]any{"name": "alpha", "delay_seconds": float64(30), "fifo_queue": false},
		},
	}}}
	got := classifyPlan(p)
	if len(got) != 1 {
		t.Fatalf("classifications=%d, want 1", len(got))
	}
	if len(got[0].driftAttrs) != 1 || got[0].driftAttrs[0] != "delay_seconds" {
		t.Errorf("driftAttrs=%v, want [delay_seconds]", got[0].driftAttrs)
	}
}

// TestClassifyPlan_ReplaceFlagged pins that a delete-create pair is
// surfaced as mustReplace, not silently treated as drift. Auto-resolving
// a replace would risk data loss.
func TestClassifyPlan_ReplaceFlagged(t *testing.T) {
	t.Parallel()
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change: &tfjson.Change{
			Actions:      tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate},
			ReplacePaths: []any{"name"},
		},
	}}}
	got := classifyPlan(p)
	if len(got) != 1 || !got[0].mustReplace || got[0].mustDelete {
		t.Errorf("got=%+v, want mustReplace=true mustDelete=false", got)
	}
}

// TestClassifyPlan_DeleteFlagged pins that a bare delete is fatal —
// import-only runs must never produce a delete, so the loop must
// surface this rather than hide it as drift.
func TestClassifyPlan_DeleteFlagged(t *testing.T) {
	t.Parallel()
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change:  &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionDelete}},
	}}}
	got := classifyPlan(p)
	if len(got) != 1 || !got[0].mustDelete {
		t.Errorf("got=%+v, want mustDelete=true", got)
	}
}

func TestClassifyPlan_NilPlanReturnsNil(t *testing.T) {
	t.Parallel()
	if got := classifyPlan(nil); got != nil {
		t.Errorf("got=%v, want nil for nil plan", got)
	}
}

// TestClassifyPlan_SortedDriftAttrs pins determinism: re-running drift
// classification on the same plan must produce byte-identical output.
// A mutation that walked the After map without sorting would still
// "pass" individual cases by coincidence but break the stability
// detector in driftfix.go (which DeepEquals adjacent iterations).
func TestClassifyPlan_SortedDriftAttrs(t *testing.T) {
	t.Parallel()
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change: &tfjson.Change{
			Actions: tfjson.Actions{tfjson.ActionUpdate},
			Before:  map[string]any{"a": 1, "b": 1, "c": 1},
			After:   map[string]any{"a": 2, "b": 2, "c": 2},
		},
	}}}
	got := classifyPlan(p)
	want := []string{"a", "b", "c"}
	if len(got[0].driftAttrs) != len(want) {
		t.Fatalf("len=%d, want %d", len(got[0].driftAttrs), len(want))
	}
	for i, w := range want {
		if got[0].driftAttrs[i] != w {
			t.Errorf("driftAttrs[%d]=%q, want %q (sorted)", i, got[0].driftAttrs[i], w)
		}
	}
}

func TestApplyDriftPatches_DropsDriftingAttrsOnly(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name           = "alpha"
  delay_seconds  = 0
  fifo_queue     = false
}
`)
	cs := []driftClassification{{
		address:    "aws_sqs_queue.x",
		driftAttrs: []string{"delay_seconds"},
	}}
	out, err := applyDriftPatches(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "delay_seconds") {
		t.Errorf("delay_seconds must be dropped\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^\s*name\s*=\s*"alpha"`).MatchString(got) {
		t.Errorf("non-drifting attr `name` must be retained (any whitespace)\n--- got ---\n%s", got)
	}
}

// TestApplyDriftPatches_NoUpdatesShortCircuits pins that the function
// returns the input bytes verbatim when no classification carries
// drift. Without this, every plan iteration would re-parse + re-emit
// the HCL, normalizing whitespace and risking unintended diffs.
func TestApplyDriftPatches_NoUpdatesShortCircuits(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" { name = "alpha" }
`)
	out, err := applyDriftPatches(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("no-updates path must short-circuit; bytes diverged\n--- want ---\n%s\n--- got ---\n%s", in, out)
	}
}

// TestApplyDriftPatches_AddressMismatchLeavesBlockAlone pins that the
// patch only touches the resource block matching the classification's
// address. A mutation that broadened to "every resource block" would
// blow away unrelated resources' attrs.
func TestApplyDriftPatches_AddressMismatchLeavesBlockAlone(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "alpha" {
  name = "a"
}

resource "aws_sqs_queue" "bravo" {
  name = "b"
}
`)
	cs := []driftClassification{{address: "aws_sqs_queue.alpha", driftAttrs: []string{"name"}}}
	out, err := applyDriftPatches(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	// alpha's `name = "a"` must be dropped, bravo's `name = "b"` must
	// remain. Use the value to disambiguate without depending on
	// hclwrite's whitespace.
	if strings.Contains(got, `"a"`) {
		t.Errorf("alpha's `name = \"a\"` must be dropped\n--- got ---\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^\s*name\s*=\s*"b"`).MatchString(got) {
		t.Errorf("bravo's `name = \"b\"` must survive\n--- got ---\n%s", got)
	}
}

// TestApplyDriftPatches_MultipleAttrsAllDropped pins the loop in the
// patch function — mutation that exited after the first attribute
// would survive single-attr cases.
func TestApplyDriftPatches_MultipleAttrsAllDropped(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name           = "alpha"
  delay_seconds  = 0
  fifo_queue     = false
}
`)
	cs := []driftClassification{{
		address:    "aws_sqs_queue.x",
		driftAttrs: []string{"delay_seconds", "fifo_queue"},
	}}
	out, err := applyDriftPatches(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "delay_seconds") {
		t.Errorf("delay_seconds must be dropped\n--- got ---\n%s", got)
	}
	if strings.Contains(got, "fifo_queue") {
		t.Errorf("fifo_queue must be dropped\n--- got ---\n%s", got)
	}
}
