package driftfix

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	tfjson "github.com/hashicorp/terraform-json"
)

// scriptedRunner returns a different plan on each iteration so tests can
// drive the loop through realistic shapes (drift→clean, stable drift,
// replace, etc.) without a terraform binary. plansByCall is consumed
// front-to-back; once exhausted, every subsequent PlanTo returns
// hasChanges=false.
type scriptedRunner struct {
	plansByCall []*tfjson.Plan
	planErr     error
	validateErr error

	planCalls     int
	showCalls     int
	validateCalls int
	calls         []string
}

func (r *scriptedRunner) PlanTo(_ context.Context, planFile string) (bool, error) {
	r.calls = append(r.calls, "plan")
	r.planCalls++
	if r.planErr != nil {
		return false, r.planErr
	}
	// Mark the plan file with the call index so ShowPlan can fish out
	// the matching scripted plan.
	idx := r.planCalls - 1
	if idx >= len(r.plansByCall) {
		return false, nil // no more changes; loop exits
	}
	if !planHasNonNoOp(r.plansByCall[idx]) {
		return false, nil
	}
	_ = os.WriteFile(planFile, []byte{byte(idx)}, 0o644)
	return true, nil
}

func (r *scriptedRunner) ShowPlan(_ context.Context, planFile string) (*tfjson.Plan, error) {
	r.calls = append(r.calls, "show")
	r.showCalls++
	body, _ := os.ReadFile(planFile)
	if len(body) == 0 {
		return nil, errors.New("plan file empty")
	}
	idx := int(body[0])
	if idx >= len(r.plansByCall) {
		return nil, errors.New("scripted plan index out of range")
	}
	return r.plansByCall[idx], nil
}

func (r *scriptedRunner) Validate(_ context.Context) error {
	r.calls = append(r.calls, "validate")
	r.validateCalls++
	return r.validateErr
}

func planHasNonNoOp(p *tfjson.Plan) bool {
	if p == nil {
		return false
	}
	for _, rc := range p.ResourceChanges {
		if rc != nil && rc.Change != nil && !isNoOp(rc.Change.Actions) {
			return true
		}
	}
	return false
}

func updatePlan(addr string, before, after map[string]any) *tfjson.Plan {
	return &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: addr,
		Change: &tfjson.Change{
			Actions: tfjson.Actions{tfjson.ActionUpdate},
			Before:  before,
			After:   after,
		},
	}}}
}

func emptyPlan() *tfjson.Plan {
	return &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change:  &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionNoop}},
	}}}
}

func writeFixture(t *testing.T, dir string) {
	t.Helper()
	body := []byte(`resource "aws_sqs_queue" "x" {
  name          = "alpha"
  delay_seconds = 0
}
`)
	if err := os.WriteFile(filepath.Join(dir, generatedFile), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRun_EmptyPlanFirstIterationReturnsImmediately(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{emptyPlan()}}
	res, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if res.Iterations != 1 {
		t.Errorf("Iterations=%d, want 1", res.Iterations)
	}
	if runner.planCalls != 1 || runner.showCalls != 0 || runner.validateCalls != 0 {
		t.Errorf("call counts plan/show/validate = %d/%d/%d, want 1/0/0",
			runner.planCalls, runner.showCalls, runner.validateCalls)
	}
}

// TestRun_ImportOnlyPlanReturnsClean pins the post-2b convergence
// shape: terraform-exec's Plan reports hasChanges=true for any plan
// that imports resources, even when there's no actual drift. The loop
// must short-circuit when classifications is empty (only no-ops past
// the import) — otherwise it'd flag a clean import as stuck drift and
// fail the run. This was the live-smoke regression that prompted the
// short-circuit; pinning it here keeps that mistake from coming back.
func TestRun_ImportOnlyPlanReturnsClean(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	// 5 imported resources, all with no-op Change.Actions — the
	// realistic shape of a Stage 2b output that's already drift-free.
	rcs := make([]*tfjson.ResourceChange, 0, 5)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		rcs = append(rcs, &tfjson.ResourceChange{
			Address: "aws_sqs_queue." + name,
			Change:  &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionNoop}},
		})
	}
	plan := &tfjson.Plan{ResourceChanges: rcs}
	// alwaysHasChanges forces PlanTo to return (true, nil) regardless
	// of whether the plan has non-noop actions — the behavior real
	// terraform-exec exhibits for an import-only plan.
	runner := &alwaysHasChangesRunner{scriptedRunner: scriptedRunner{plansByCall: []*tfjson.Plan{plan}}}
	res, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if res.Iterations != 1 {
		t.Errorf("Iterations=%d, want 1 (clean plan converges immediately)", res.Iterations)
	}
	// Validate must NOT run on the early-return path: no patch was
	// written, so there's nothing to validate. A mutation that always
	// validated would mask a logic bug where the loop wrote a partial
	// patch before short-circuiting.
	if runner.validateCalls != 0 {
		t.Errorf("validate must not run on early-return path; got %d calls", runner.validateCalls)
	}
}

// alwaysHasChangesRunner forces PlanTo to report hasChanges=true even
// when every Action is no-op. Models the real-world shape of
// terraform-exec.Plan against an import-only stack.
type alwaysHasChangesRunner struct {
	scriptedRunner
}

func (r *alwaysHasChangesRunner) PlanTo(_ context.Context, planFile string) (bool, error) {
	r.calls = append(r.calls, "plan")
	r.planCalls++
	idx := r.planCalls - 1
	if idx >= len(r.plansByCall) {
		return false, nil
	}
	_ = os.WriteFile(planFile, []byte{byte(idx)}, 0o644)
	return true, nil
}

// TestRun_DriftThenCleanConverges pins the happy-path loop: iteration 1
// returns drift, the patch drops the offending attr, iteration 2's plan
// is empty, Run exits with Iterations==2. A mutation that re-ran the
// plan without applying the patch first would surface the same drift on
// iteration 2 and trigger the stability detector.
func TestRun_DriftThenCleanConverges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{
		updatePlan("aws_sqs_queue.x",
			map[string]any{"name": "alpha", "delay_seconds": float64(30)},
			map[string]any{"name": "alpha", "delay_seconds": float64(0)}),
		emptyPlan(),
	}}
	res, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if res.Iterations != 2 {
		t.Errorf("Iterations=%d, want 2", res.Iterations)
	}
	body, _ := os.ReadFile(filepath.Join(dir, generatedFile))
	if hclResourceHasAttr(t, body, "aws_sqs_queue.x", "delay_seconds") {
		t.Errorf("delay_seconds must be dropped after patch\n--- got ---\n%s", body)
	}
	if runner.validateCalls != 1 {
		t.Errorf("validate must run once per patched iteration; got %d", runner.validateCalls)
	}
}

func TestRun_StreamsIterationProgressToStdout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{
		updatePlan("aws_sqs_queue.x",
			map[string]any{"name": "alpha", "delay_seconds": float64(30)},
			map[string]any{"name": "alpha", "delay_seconds": float64(0)}),
		emptyPlan(),
	}}
	var progress strings.Builder

	_, err := Run(context.Background(), Options{
		Workdir: dir,
		Runner:  runner,
		Stdout:  &progress,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := progress.String()
	assertContainsInOrder(t, got, []string{
		"driftfix: starting drift convergence",
		"driftfix: iteration 1: running terraform plan",
		"driftfix: iteration 1: reading plan details",
		"driftfix: iteration 1: patching 1 drift attribute(s) across 1 resource(s)",
		"driftfix: iteration 1: validating patch",
		"driftfix: iteration 2: running terraform plan",
		"driftfix: converged after 2 iteration(s)",
	})
}

func TestRun_NilStdoutDoesNotWriteProgressToProcessStdout(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir)
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{emptyPlan()}}

	stdout := captureStdout(t, func() {
		_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
		if err != nil {
			t.Fatal(err)
		}
	})
	if stdout != "" {
		t.Fatalf("nil Stdout wrote to process stdout; got:\n%s", stdout)
	}
}

func assertContainsInOrder(t *testing.T, got string, wants []string) {
	t.Helper()
	pos := 0
	for _, want := range wants {
		idx := strings.Index(got[pos:], want)
		if idx < 0 {
			t.Fatalf("progress stream missing %q after byte %d; got:\n%s", want, pos, got)
		}
		pos += idx + len(want)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// hclResourceHasAttr parses raw HCL and reports whether the named
// resource address has the named top-level attribute. Robust to
// formatter whitespace and comments.
func hclResourceHasAttr(t *testing.T, raw []byte, address, attr string) bool {
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
		if len(labels) != 2 || labels[0]+"."+labels[1] != address {
			continue
		}
		return blk.Body().GetAttribute(attr) != nil
	}
	return false
}

// TestRun_RecurringDriftEscalatesToIgnoreChanges pins the two-strategy
// loop: when the same drift recurs after the drop pass (iter 2's plan
// matches iter 1's), the loop escalates to lifecycle.ignore_changes
// instead of failing. Iter 3 sees no drift → converges. This is the
// real-world Stage 2c1 case for CREATE-only / DESTROY-only schema
// attributes (e.g. aws_secretsmanager_secret.recovery_window_in_days)
// whose schema default differs from the imported cloud state's "null".
func TestRun_RecurringDriftEscalatesToIgnoreChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	driftP := updatePlan("aws_sqs_queue.x",
		map[string]any{"name": "alpha"},
		map[string]any{"name": "bravo"})
	// iter1=drift → drop; iter2=same drift → escalate to ignore_changes;
	// iter3=empty → converge.
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{driftP, driftP, emptyPlan()}}
	var progress strings.Builder
	res, err := Run(context.Background(), Options{Workdir: dir, Runner: runner, Stdout: &progress})
	if err != nil {
		t.Fatalf("expected escalation+convergence; got: %v", err)
	}
	if res.Iterations != 3 {
		t.Errorf("Iterations=%d, want 3 (drop, escalate, converge)", res.Iterations)
	}
	body, _ := os.ReadFile(filepath.Join(dir, generatedFile))
	if !hclResourceHasIgnoreChange(t, body, "aws_sqs_queue.x", "name") {
		t.Errorf("escalation must add `name` to lifecycle.ignore_changes\n--- got ---\n%s", body)
	}
	// Validate must run TWICE: once after the drop patch, once after
	// the escalation. A mutation that skipped the escalation-side
	// validate would survive without this check (the test would pass
	// even if a Required attr were dropped during escalation).
	if runner.validateCalls != 2 {
		t.Errorf("validate calls = %d, want 2 (one per patched iteration)", runner.validateCalls)
	}
	// Pin call ordering so a mutation that flipped patch+validate
	// wouldn't slip past. Expected: plan, show, validate, plan, show,
	// validate, plan, (no show on clean plan).
	wantOrder := []string{"plan", "show", "validate", "plan", "show", "validate", "plan"}
	if !equalStringSlices(runner.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", runner.calls, wantOrder)
	}
	assertContainsInOrder(t, progress.String(), []string{
		"driftfix: iteration 1: patching 1 drift attribute(s) across 1 resource(s)",
		"driftfix: iteration 1: validating patch",
		"driftfix: iteration 2: running terraform plan",
		"driftfix: iteration 2: reading plan details",
		"driftfix: iteration 2: escalating 1 drift attribute(s) across 1 resource(s) to ignore_changes",
		"driftfix: iteration 2: validating ignore_changes patch",
		"driftfix: iteration 3: running terraform plan",
		"driftfix: converged after 3 iteration(s)",
	})
}

// TestRun_DriftStableAfterEscalationFatal pins the truly-stuck case:
// drift recurs even AFTER ignore_changes has been added for those
// attrs. The loop must surface the issue as fatal rather than spin.
func TestRun_DriftStableAfterEscalationFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	driftP := updatePlan("aws_sqs_queue.x",
		map[string]any{"name": "alpha"},
		map[string]any{"name": "bravo"})
	// Three identical plans. iter1 drops, iter2 escalates, iter3 still
	// drifts → fatal "stable but unresolved."
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{driftP, driftP, driftP}}
	_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err == nil {
		t.Fatal("expected stable-after-escalation error")
	}
	if !strings.Contains(err.Error(), "stable but unresolved") {
		t.Errorf("err=%v, want stable-but-unresolved message", err)
	}
	// Pin that the escalation patch DID run before the fatal: the
	// disk file must contain ignore_changes at the time of failure.
	// A mutation that returned the fatal without performing the
	// escalation patch on iter 2 would survive a message-only check.
	body, _ := os.ReadFile(filepath.Join(dir, generatedFile))
	if !hclResourceHasIgnoreChange(t, body, "aws_sqs_queue.x", "name") {
		t.Errorf("escalation must have run (writing ignore_changes) before fatal\n--- got ---\n%s", body)
	}
}

// TestRun_ValidateFailureAfterEscalationFatal pins that validate
// failure on the escalation path surfaces the escalation-specific
// error message, distinct from validate failure on the drop path.
// A mutation that hard-coded "validate after patch" everywhere would
// survive the drop-path-only TestRun_ValidateFailureFatal.
func TestRun_ValidateFailureAfterEscalationFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	driftP := updatePlan("aws_sqs_queue.x",
		map[string]any{"name": "alpha"},
		map[string]any{"name": "bravo"})
	// failOnNthValidate fails ONLY the second validate (the one on
	// the escalation path). The first validate (drop pass) succeeds.
	runner := &validateNthFailRunner{
		scriptedRunner: scriptedRunner{plansByCall: []*tfjson.Plan{driftP, driftP, emptyPlan()}},
		failOnCall:     2,
		failErr:        errors.New("hcl unexpected token"),
	}
	_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner.toBase()})
	if err == nil {
		t.Fatal("expected validate error on escalation path")
	}
	if !strings.Contains(err.Error(), "validate after ignore_changes") {
		t.Errorf("err=%v, want escalation-specific validate message", err)
	}
}

// validateNthFailRunner returns failErr on the failOnCall-th call to
// Validate (1-indexed). All other Validate calls succeed.
type validateNthFailRunner struct {
	scriptedRunner
	failOnCall int
	failErr    error
}

func (r *validateNthFailRunner) Validate(_ context.Context) error {
	r.calls = append(r.calls, "validate")
	r.validateCalls++
	if r.validateCalls == r.failOnCall {
		return r.failErr
	}
	return nil
}

// toBase returns the runner as a terraformRunner — needed because Go
// embeds the base struct's methods but Run takes the interface.
func (r *validateNthFailRunner) toBase() terraformRunner { return r }

// equalStringSlices reports whether two string slices are equal.
// Used for pinning call sequence order without a third-party diff
// library.
func equalStringSlices(a, b []string) bool {
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

// TestRun_ReplaceFatal pins that a plan with a delete-create pair never
// auto-resolves — the operator must reconcile manually because a
// replace on an imported resource = data loss.
func TestRun_ReplaceFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	pre, _ := os.ReadFile(filepath.Join(dir, generatedFile))
	replaceP := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change: &tfjson.Change{
			Actions:      tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate},
			ReplacePaths: []any{"name"},
		},
	}}}
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{replaceP}}
	_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err == nil {
		t.Fatal("expected replace error")
	}
	if !strings.Contains(err.Error(), "must be replaced") {
		t.Errorf("err=%v, want must-be-replaced message", err)
	}
	// generated.tf must be untouched on a fatal — a mutation that
	// patched before classifying as fatal would silently corrupt the
	// operator's only debugging artifact.
	post, _ := os.ReadFile(filepath.Join(dir, generatedFile))
	if string(pre) != string(post) {
		t.Errorf("generated.tf must be byte-identical on fatal exit\n--- pre ---\n%s\n--- post ---\n%s", pre, post)
	}
}

// TestRun_DeleteFatal pins that a bare delete is fatal.
func TestRun_DeleteFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	pre, _ := os.ReadFile(filepath.Join(dir, generatedFile))
	deleteP := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{{
		Address: "aws_sqs_queue.x",
		Change:  &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionDelete}},
	}}}
	runner := &scriptedRunner{plansByCall: []*tfjson.Plan{deleteP}}
	_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err == nil {
		t.Fatal("expected delete error")
	}
	if !strings.Contains(err.Error(), "marked for delete") {
		t.Errorf("err=%v, want marked-for-delete message", err)
	}
	post, _ := os.ReadFile(filepath.Join(dir, generatedFile))
	if string(pre) != string(post) {
		t.Errorf("generated.tf must be byte-identical on fatal exit\n--- pre ---\n%s\n--- post ---\n%s", pre, post)
	}
}

// hclResourceHasIgnoreChange parses raw HCL and reports whether the
// named resource address has an attr in lifecycle.ignore_changes.
// Use this rather than strings.Contains so the assertion isn't
// fragile to formatter whitespace, comments, or attr ordering.
func hclResourceHasIgnoreChange(t *testing.T, raw []byte, address, attr string) bool {
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
		if len(labels) != 2 || labels[0]+"."+labels[1] != address {
			continue
		}
		for _, sub := range blk.Body().Blocks() {
			if sub.Type() != "lifecycle" {
				continue
			}
			ic := sub.Body().GetAttribute("ignore_changes")
			if ic == nil {
				continue
			}
			if slices.Contains(parseIgnoreChangesList(ic), attr) {
				return true
			}
		}
	}
	return false
}

// TestRun_ValidateFailureFatal pins that if patching breaks `terraform
// validate` (e.g. dropped a Required attr), the loop surfaces the
// underlying validate error rather than continuing into another plan.
func TestRun_ValidateFailureFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	runner := &scriptedRunner{
		plansByCall: []*tfjson.Plan{updatePlan("aws_sqs_queue.x",
			map[string]any{"name": "alpha"},
			map[string]any{"name": "bravo"})},
		validateErr: errors.New(`"name" is required`),
	}
	_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err == nil {
		t.Fatal("expected validate error")
	}
	if !strings.Contains(err.Error(), "validate after patch") || !strings.Contains(err.Error(), "is required") {
		t.Errorf("err=%v, want validate+required message", err)
	}
}

// TestRun_MaxIterationsExhausted pins the bound: the loop never spins
// forever even if drift keeps changing shape across iterations. Default
// is defaultMaxIterations; lower it explicitly here so the test runs
// quickly.
func TestRun_MaxIterationsExhausted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	// Distinct drift shapes each iteration so the stability detector
	// never fires. Three iterations + max=2 means iteration 2 is the
	// last one we run; the loop returns the "iterations exhausted"
	// error rather than continuing.
	plans := []*tfjson.Plan{
		updatePlan("aws_sqs_queue.x", map[string]any{"a": 1}, map[string]any{"a": 2}),
		updatePlan("aws_sqs_queue.x", map[string]any{"b": 1}, map[string]any{"b": 2}),
		updatePlan("aws_sqs_queue.x", map[string]any{"c": 1}, map[string]any{"c": 2}),
	}
	runner := &scriptedRunner{plansByCall: plans}
	_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner, MaxIterations: 2})
	if err == nil {
		t.Fatal("expected iterations-exhausted error")
	}
	if !strings.Contains(err.Error(), "iterations exhausted") {
		t.Errorf("err=%v, want iterations-exhausted message", err)
	}
}

func TestRun_RejectsEmptyWorkdir(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Options{Runner: &scriptedRunner{}})
	if err == nil {
		t.Error("expected error for missing workdir")
	}
}

// TestRun_PlanErrorPropagates pins that a real plan failure (not just
// "drift detected", which shows up as hasChanges=true) surfaces
// verbatim and aborts the loop.
func TestRun_PlanErrorPropagates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir)
	runner := &scriptedRunner{planErr: errors.New("AccessDenied on terraform plan")}
	_, err := Run(context.Background(), Options{Workdir: dir, Runner: runner})
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err=%v, want AccessDenied", err)
	}
}
