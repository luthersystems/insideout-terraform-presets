package driftfix

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
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

// TestApplyDriftPatches_MalformedHCLReturnsError pins the parse-error
// path. A mutation that swallowed parse diagnostics and returned the
// unparsed bytes would surface as silent drift the loop couldn't
// patch — surface as a real error so the operator sees it on the
// first iteration instead of via "iterations exhausted."
func TestApplyDriftPatches_MalformedHCLReturnsError(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" { name = "alpha" oops`)
	cs := []driftClassification{{address: "aws_sqs_queue.x", driftAttrs: []string{"name"}}}
	_, err := applyDriftPatches(in, cs)
	if err == nil {
		t.Fatal("expected parse error for malformed HCL")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("err=%v, want parse-error message", err)
	}
}

// TestClassifyPlan_RemovedKeyIsDrift pins the union-walk semantics of
// topLevelDrift: a key present in Before but absent from After must
// classify as drift. Without this, the loop would short-circuit on
// `len(classifications)==0` and report "clean" while terraform plan
// still reports a change. terraform-json typically emits null for
// unset attrs (not absent), so this is a defensive pin against future
// provider releases that drop a key entirely.
func TestClassifyPlan_RemovedKeyIsDrift(t *testing.T) {
	t.Parallel()
	p := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change: &tfjson.Change{
			Actions: tfjson.Actions{tfjson.ActionUpdate},
			Before:  map[string]any{"name": "alpha", "delay_seconds": float64(0)},
			After:   map[string]any{"name": "alpha"}, // delay_seconds dropped
		},
	}}}
	got := classifyPlan(p)
	if len(got) != 1 {
		t.Fatalf("classifications=%d, want 1", len(got))
	}
	if len(got[0].driftAttrs) != 1 || got[0].driftAttrs[0] != "delay_seconds" {
		t.Errorf("driftAttrs=%v, want [delay_seconds] (key removed from After)", got[0].driftAttrs)
	}
}

// hclHasIgnoreChanges reports whether the given resource address (e.g.
// "aws_sqs_queue.x") has a lifecycle.ignore_changes containing every
// attr in want. Parses with hclwrite so it isn't sensitive to formatter
// whitespace or surrounding comments — a string-contains check on the
// full body would also pass when "ignore_changes" appears in an
// unrelated comment.
func hclHasIgnoreChanges(t *testing.T, raw []byte, address string, want []string) bool {
	t.Helper()
	f, diags := hclwrite.ParseConfig(raw, "test.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatalf("parse: %s", diags.Error())
	}
	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		if labels[0]+"."+labels[1] != address {
			continue
		}
		for _, sub := range blk.Body().Blocks() {
			if sub.Type() != "lifecycle" {
				continue
			}
			attr := sub.Body().GetAttribute("ignore_changes")
			if attr == nil {
				continue
			}
			got := parseIgnoreChangesList(attr)
			gotSet := map[string]struct{}{}
			for _, g := range got {
				gotSet[g] = struct{}{}
			}
			for _, w := range want {
				if _, ok := gotSet[w]; !ok {
					return false
				}
			}
			return true
		}
	}
	return false
}

func TestApplyIgnoreChangesEscalation_AddsLifecycleBlock(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name = "alpha"
}
`)
	cs := []driftClassification{{
		address:    "aws_sqs_queue.x",
		driftAttrs: []string{"delay_seconds"},
	}}
	out, err := applyIgnoreChangesEscalation(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	if !hclHasIgnoreChanges(t, out, "aws_sqs_queue.x", []string{"delay_seconds"}) {
		t.Errorf("escalation must add lifecycle.ignore_changes containing delay_seconds\n--- got ---\n%s", out)
	}
}

// TestApplyIgnoreChangesEscalation_MergesIntoExistingLifecycle pins that
// an existing ignore_changes list is preserved (entries are merged,
// not overwritten). A mutation that always created a new lifecycle
// block would produce HCL with two lifecycle blocks (parse error) or
// would clobber whatever the cleanup pass already wrote (e.g. lambda
// fixup's filename + image_uri pins).
func TestApplyIgnoreChangesEscalation_MergesIntoExistingLifecycle(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name = "alpha"

  lifecycle {
    ignore_changes = [filename]
  }
}
`)
	cs := []driftClassification{{
		address:    "aws_sqs_queue.x",
		driftAttrs: []string{"delay_seconds"},
	}}
	out, err := applyIgnoreChangesEscalation(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	if !hclHasIgnoreChanges(t, out, "aws_sqs_queue.x", []string{"filename", "delay_seconds"}) {
		t.Errorf("merge must preserve `filename` and add `delay_seconds`\n--- got ---\n%s", out)
	}
	// Counting `lifecycle {` blocks via parse: there must be exactly 1.
	f, diags := hclwrite.ParseConfig(out, "test.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatalf("parse: %s", diags.Error())
	}
	count := 0
	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		for _, sub := range blk.Body().Blocks() {
			if sub.Type() == "lifecycle" {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("lifecycle block count = %d, want 1 (merge, not append)", count)
	}
}

// TestApplyIgnoreChangesEscalation_DedupesExistingEntries pins that an
// attr already in ignore_changes isn't duplicated when escalation
// re-adds it. Duplicate entries are accepted by terraform but ugly
// in generated.tf and a sign of buggy merge logic.
func TestApplyIgnoreChangesEscalation_DedupesExistingEntries(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name = "alpha"

  lifecycle {
    ignore_changes = [delay_seconds]
  }
}
`)
	cs := []driftClassification{{
		address:    "aws_sqs_queue.x",
		driftAttrs: []string{"delay_seconds"},
	}}
	out, err := applyIgnoreChangesEscalation(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(out), "delay_seconds"); c != 1 {
		t.Errorf("delay_seconds occurrences = %d, want 1 (dedup)\n--- got ---\n%s", c, out)
	}
}

// TestApplyIgnoreChangesEscalation_AddressMismatchLeavesBlockAlone pins
// that escalation only touches the resource block whose address is in
// the classification — so a single drifting resource doesn't sprinkle
// lifecycle blocks across every other resource in the file.
func TestApplyIgnoreChangesEscalation_AddressMismatchLeavesBlockAlone(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "alpha" {
  name = "a"
}

resource "aws_sqs_queue" "bravo" {
  name = "b"
}
`)
	cs := []driftClassification{{address: "aws_sqs_queue.alpha", driftAttrs: []string{"delay_seconds"}}}
	out, err := applyIgnoreChangesEscalation(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	if !hclHasIgnoreChanges(t, out, "aws_sqs_queue.alpha", []string{"delay_seconds"}) {
		t.Errorf("alpha must gain lifecycle.ignore_changes\n--- got ---\n%s", out)
	}
	if hclHasIgnoreChanges(t, out, "aws_sqs_queue.bravo", []string{"delay_seconds"}) {
		t.Errorf("bravo must NOT gain lifecycle.ignore_changes\n--- got ---\n%s", out)
	}
}

// TestApplyIgnoreChangesEscalation_MultipleAttrsAllAdded pins the loop —
// a mutation that exited after the first attr would survive single-
// attr cases. Also pins the sort: output must be deterministic so
// downstream golden-file tests don't churn.
func TestApplyIgnoreChangesEscalation_MultipleAttrsAllAdded(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" {
  name = "alpha"
}
`)
	cs := []driftClassification{{
		address:    "aws_sqs_queue.x",
		driftAttrs: []string{"fifo_queue", "delay_seconds"},
	}}
	out, err := applyIgnoreChangesEscalation(in, cs)
	if err != nil {
		t.Fatal(err)
	}
	if !hclHasIgnoreChanges(t, out, "aws_sqs_queue.x", []string{"delay_seconds", "fifo_queue"}) {
		t.Errorf("both attrs must appear in ignore_changes\n--- got ---\n%s", out)
	}
	// Pin sort: delay_seconds < fifo_queue alphabetically.
	if !regexp.MustCompile(`ignore_changes\s*=\s*\[delay_seconds,\s*fifo_queue\]`).Match(out) {
		t.Errorf("ignore_changes must be sorted [delay_seconds, fifo_queue]\n--- got ---\n%s", out)
	}
}

func TestApplyIgnoreChangesEscalation_NoUpdatesShortCircuits(t *testing.T) {
	t.Parallel()
	in := []byte(`resource "aws_sqs_queue" "x" { name = "alpha" }
`)
	out, err := applyIgnoreChangesEscalation(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("no-updates path must short-circuit; bytes diverged")
	}
}

func TestUniqueSorted(t *testing.T) {
	t.Parallel()
	got := uniqueSorted([]string{"b", "a", "b", "c", "a"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d]=%q, want %q", i, got[i], w)
		}
	}
}
