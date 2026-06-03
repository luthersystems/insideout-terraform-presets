// Package driftfix runs after Stage 2b's genconfig pipeline produces a
// validating generated.tf. It loops `terraform plan` against the stack
// and patches drifting attributes — dropping them from generated.tf so
// terraform reads the value from cloud state instead of trying to push
// the import-time snapshot back. Loop terminates when the plan is empty
// or the same drift recurs across iterations (stability without
// resolution → fatal).
//
// Stage 2c1 of the #189 split. Dependency chase (Stage 2c3) and the
// localstack zero-drift CI gate (Stage 2c4) layer on top.
package driftfix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"golang.org/x/sync/errgroup"
)

// generatedFile is the file genconfig wrote and driftfix mutates in
// place. Kept as a constant so the orchestrator and patch helpers agree.
const generatedFile = "generated.tf"

// planFile is the name of the binary plan produced by `terraform plan
// -out=...`. ShowPlan reads it back into the typed shape.
const planFile = "driftfix.tfplan"

// defaultMaxIterations bounds the patch loop. Five is enough for the
// realistic case (one or two passes typically converges); the bound is
// here to surface "stable but unresolved" cases as a fatal rather than
// spinning on an attr we can't patch.
const defaultMaxIterations = 5

// Options is the input to Run. Workdir must be the same directory that
// genconfig.Run produced — driftfix expects generated.tf, providers.tf,
// imports.tf, and the .terraform dir already in place.
type Options struct {
	Workdir       string
	MaxIterations int
	// Runner is optional; nil means "construct an execRunner from PATH."
	Runner terraformRunner

	// Stdout, when non-nil, receives the live stdout/stderr of the
	// terraform subprocess (the per-iteration plan + validate) so a
	// long-running caller — the Mars reverse-import job — can stream
	// progress instead of going silent through the drift loop. Nil means
	// "discard subprocess output" (the historical behavior). Ignored when
	// Runner is injected.
	Stdout io.Writer

	// newRunner builds a terraformRunner for a specific stack directory.
	// It exists so the multi-region path can construct one runner per
	// region subdir (each subdir is its own plannable stack), and so
	// tests can inject per-stack fakes. nil → newExecRunner. The single
	// injected Runner above still takes precedence for the single-stack
	// path (Workdir itself is the stack); newRunner is consulted for the
	// per-region subdirs. Unexported because production callers
	// (run.go/discover.go) never set it — they always want the real
	// execRunner — and only the package's own tests inject a fake.
	newRunner func(workdir string, stdout io.Writer) (terraformRunner, error)
}

// runnerFor returns the terraformRunner to drive the stack rooted at
// stackDir. The single injected Runner wins only for the single-stack path
// (stackDir == Workdir); every other stack (the per-region subdirs) goes
// through newRunner, falling back to the real execRunner.
func (opts Options) runnerFor(stackDir string, out io.Writer) (terraformRunner, error) {
	if opts.Runner != nil && stackDir == opts.Workdir {
		return opts.Runner, nil
	}
	if opts.newRunner != nil {
		return opts.newRunner(stackDir, out)
	}
	return newExecRunner(stackDir, out)
}

// Result is what the orchestrator hands back to the caller. Iterations
// is informational (operator-visible in logs); GeneratedPath points at
// the file the loop converged on.
type Result struct {
	GeneratedPath string
	Iterations    int
}

// Run is the Stage 2c1 pipeline:
//
//  1. Plan the existing stack.
//  2. If plan is empty (no resource_changes off no-op), return.
//  3. Classify changes; replace/delete = fatal.
//  4. Drop drifting top-level attrs from generated.tf.
//  5. Validate the patched file (catches Required-attr removal).
//  6. Goto 1, up to MaxIterations.
//
// "Stable but unresolved" — same drift recurs after a patch — surfaces
// as a fatal so the operator sees what couldn't be fixed instead of
// looping forever.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Workdir == "" {
		return nil, fmt.Errorf("driftfix: Workdir required")
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = defaultMaxIterations
	}
	abs, err := filepath.Abs(opts.Workdir)
	if err != nil {
		return nil, fmt.Errorf("abs workdir: %w", err)
	}
	opts.Workdir = abs

	// Multi-region: genconfig emits one plannable stack per region under
	// region-<alias>/ subdirs and leaves only a debug-concat generated.tf at
	// the parent (no providers.tf, no .terraform). Drift-fix each region
	// subdir independently — they share no state — then re-merge the parent
	// concat. A single-region run (or any layout where the Workdir itself is
	// the plannable stack) takes the historical single-stack path unchanged.
	stacks := plannableStacks(opts.Workdir)
	if len(stacks) == 1 && stacks[0] == opts.Workdir {
		progressf(opts.Stdout, "driftfix: starting drift convergence (max %d iteration(s))…\n", opts.MaxIterations)
		return runStack(ctx, opts, opts.Workdir, opts.Stdout)
	}
	return runMultiStack(ctx, opts, stacks)
}

// runStack runs the Stage 2c1 drift-convergence loop against a single
// plannable stack rooted at stackDir, streaming progress to out. It is the
// historical Run body, parameterized by stack directory so the multi-region
// path can drive one loop per region subdir.
func runStack(ctx context.Context, opts Options, stackDir string, out io.Writer) (*Result, error) {
	// Route this stack's progress (and the terraform subprocess output the
	// runner streams) to the caller-provided sink, which for the multi-region
	// path is a per-stack writer that serializes whole lines.
	opts.Stdout = out
	runner, err := opts.runnerFor(stackDir, out)
	if err != nil {
		return nil, err
	}

	generatedPath := filepath.Join(stackDir, generatedFile)
	planPath := filepath.Join(stackDir, planFile)

	var prevDrift map[string][]string
	// alreadyEscalated tracks (address, attr) pairs we've already moved
	// to lifecycle.ignore_changes. If the same drift recurs AFTER
	// escalation, it's truly stable-unresolved and we surface it as
	// fatal — the operator must inspect manually.
	alreadyEscalated := map[string]map[string]struct{}{}

	for iter := 1; iter <= opts.MaxIterations; iter++ {
		progressf(opts.Stdout, "driftfix: iteration %d: running terraform plan…\n", iter)
		hasChanges, err := runner.PlanTo(ctx, planPath)
		if err != nil {
			return nil, fmt.Errorf("driftfix iter %d: terraform plan: %w", iter, err)
		}
		if !hasChanges {
			progressf(opts.Stdout, "driftfix: converged after %d iteration(s)\n", iter)
			return &Result{GeneratedPath: generatedPath, Iterations: iter}, nil
		}

		progressf(opts.Stdout, "driftfix: iteration %d: reading plan details…\n", iter)
		plan, err := runner.ShowPlan(ctx, planPath)
		if err != nil {
			return nil, fmt.Errorf("driftfix iter %d: show plan: %w", iter, err)
		}

		classifications := classifyPlan(plan)
		if fatalErr := classificationFatal(classifications); fatalErr != nil {
			return nil, fmt.Errorf("driftfix iter %d: %w", iter, fatalErr)
		}
		// terraform-exec reports hasChanges=true for an import-only plan
		// because imports count as changes. classifyPlan filters those
		// out via the no-op check; if nothing actionable remains, the
		// plan is functionally clean and the loop is done.
		if len(classifications) == 0 {
			progressf(opts.Stdout, "driftfix: iteration %d: plan has no actionable drift; converged\n", iter)
			return &Result{GeneratedPath: generatedPath, Iterations: iter}, nil
		}

		curDrift := driftMap(classifications)
		raw, err := os.ReadFile(generatedPath)
		if err != nil {
			return nil, fmt.Errorf("driftfix iter %d: read generated.tf: %w", iter, err)
		}

		// Stability detection: same drift two iterations in a row → our
		// drop-from-HCL patch isn't enough. Either upgrade to
		// lifecycle.ignore_changes (the right move for CREATE-only /
		// DESTROY-only schema attrs whose default differs from cloud
		// "missing"), or — if we already escalated this same drift —
		// give up and surface the error.
		if iter > 1 && reflect.DeepEqual(prevDrift, curDrift) {
			if everyDriftAttrAlreadyEscalated(curDrift, alreadyEscalated) {
				return nil, fmt.Errorf("driftfix iter %d: %w", iter, errStableUnresolved(curDrift))
			}
			progressf(opts.Stdout, "driftfix: iteration %d: escalating %d drift attribute(s) across %d resource(s) to ignore_changes…\n",
				iter, driftAttrCount(curDrift), len(curDrift))
			patched, err := applyIgnoreChangesEscalation(raw, classifications)
			if err != nil {
				return nil, fmt.Errorf("driftfix iter %d: ignore_changes escalation: %w", iter, err)
			}
			if err := os.WriteFile(generatedPath, patched, 0o644); err != nil {
				return nil, fmt.Errorf("driftfix iter %d: write generated.tf: %w", iter, err)
			}
			markEscalated(curDrift, alreadyEscalated)
			progressf(opts.Stdout, "driftfix: iteration %d: validating ignore_changes patch…\n", iter)
			if err := runner.Validate(ctx); err != nil {
				return nil, fmt.Errorf("driftfix iter %d: validate after ignore_changes: %w", iter, err)
			}
			prevDrift = curDrift
			continue
		}
		prevDrift = curDrift

		progressf(opts.Stdout, "driftfix: iteration %d: patching %d drift attribute(s) across %d resource(s)…\n",
			iter, driftAttrCount(curDrift), len(curDrift))
		patched, err := applyDriftPatches(raw, classifications)
		if err != nil {
			return nil, fmt.Errorf("driftfix iter %d: patch: %w", iter, err)
		}
		if err := os.WriteFile(generatedPath, patched, 0o644); err != nil {
			return nil, fmt.Errorf("driftfix iter %d: write generated.tf: %w", iter, err)
		}
		progressf(opts.Stdout, "driftfix: iteration %d: validating patch…\n", iter)
		if err := runner.Validate(ctx); err != nil {
			return nil, fmt.Errorf("driftfix iter %d: validate after patch: %w", iter, err)
		}
	}
	return nil, fmt.Errorf("driftfix: %d iterations exhausted without convergence", opts.MaxIterations)
}

// maxStackConcurrency bounds how many region subdirs drift-fix converges at
// once. Each terraform plan is memory-heavy and hits the AWS API, but parallel
// regions spread load across per-region/per-service rate limits (safer than
// parallel calls within one region), so a modest pool keeps a whole-account
// multi-region run from converging the regions strictly back-to-back without
// risking throttling or OOM.
const maxStackConcurrency = 6

// runMultiStack converges each plannable region stack concurrently (bounded by
// maxStackConcurrency), then re-concatenates the per-region generated.tf files
// into the parent debug artifact so dep-chase's text-read of the parent
// reflects the patches. Per-stack progress streams are serialized through a
// shared mutex so concurrent regions don't interleave mid-line.
func runMultiStack(ctx context.Context, opts Options, stacks []string) (*Result, error) {
	limit := min(len(stacks), maxStackConcurrency)
	progressf(opts.Stdout, "driftfix: %d regional stack(s) detected; converging each (max %d iteration(s), up to %d in parallel)…\n",
		len(stacks), opts.MaxIterations, limit)

	var mu sync.Mutex
	sink := func() io.Writer {
		if opts.Stdout == nil {
			return nil
		}
		return &syncWriter{mu: &mu, w: opts.Stdout}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	iterations := make([]int, len(stacks))
	for i, stack := range stacks {
		g.Go(func() error {
			label := filepath.Base(stack)
			out := sink()
			progressf(out, "driftfix: %s: starting…\n", label)
			res, err := runStack(gctx, opts, stack, out)
			if err != nil {
				return fmt.Errorf("driftfix %s: %w", label, err)
			}
			iterations[i] = res.Iterations
			progressf(out, "driftfix: %s: converged after %d iteration(s)\n", label, res.Iterations)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Re-merge the per-region generated.tf into the parent debug concat so
	// dep-chase (which reads the parent as text) and the on-disk artifact
	// reflect the drift patches. Reuse genconfig's merge so the parent format
	// stays identical to the one genconfig wrote on the first pass.
	parentGenerated := filepath.Join(opts.Workdir, generatedFile)
	if err := genconfig.WriteMergedGenerated(parentGenerated, stacks); err != nil {
		return nil, fmt.Errorf("driftfix: re-merge generated.tf: %w", err)
	}

	maxIter := 0
	for _, it := range iterations {
		maxIter = max(maxIter, it)
	}
	progressf(opts.Stdout, "driftfix: all %d regional stack(s) converged\n", len(stacks))
	return &Result{GeneratedPath: parentGenerated, Iterations: maxIter}, nil
}

// plannableStacks decides which directories drift-fix runs against. A stack is
// "plannable" when it carries both providers.tf and generated.tf (genconfig
// emits both into every single-region stack and every per-region subdir). The
// multi-region parent carries only the debug-concat generated.tf — no
// providers.tf — so it is NOT plannable and we descend into its
// region-<alias>/ subdirs. Resolution order:
//
//  1. Workdir itself is plannable → single-stack run ([Workdir]).
//  2. else immediate subdirs that are plannable → multi-region run.
//  3. else fall back to [Workdir] so the historical single-stack path (and
//     fake-runner tests that don't emit providers.tf) behave exactly as before
//     — runStack surfaces the real terraform error if the dir isn't runnable.
func plannableStacks(workdir string) []string {
	if isPlannable(workdir) {
		return []string{workdir}
	}
	if subs := plannableSubdirs(workdir); len(subs) > 0 {
		return subs
	}
	return []string{workdir}
}

// isPlannable reports whether dir contains both providers.tf and generated.tf
// — the marker that genconfig finished emitting a runnable stack there.
func isPlannable(dir string) bool {
	return fileExists(filepath.Join(dir, "providers.tf")) &&
		fileExists(filepath.Join(dir, generatedFile))
}

// plannableSubdirs returns the immediate subdirectories of workdir that are
// plannable stacks, sorted for deterministic ordering (matching genconfig's
// region-sorted merge order).
func plannableSubdirs(workdir string) []string {
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return nil
	}
	var subs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(workdir, e.Name())
		if isPlannable(dir) {
			subs = append(subs, dir)
		}
	}
	sort.Strings(subs)
	return subs
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// syncWriter serializes whole-line writes from concurrent per-region drift
// loops so their progress/terraform output doesn't interleave mid-line on the
// shared sink.
type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// everyDriftAttrAlreadyEscalated returns true iff every (address, attr)
// pair in `cur` was previously added to `escalated`. When that's the
// case, ignore_changes wasn't enough either — the drift is truly stuck
// and the loop must escalate to a fatal error.
func everyDriftAttrAlreadyEscalated(cur map[string][]string, escalated map[string]map[string]struct{}) bool {
	for addr, attrs := range cur {
		seen, ok := escalated[addr]
		if !ok {
			return false
		}
		for _, a := range attrs {
			if _, ok := seen[a]; !ok {
				return false
			}
		}
	}
	return true
}

// markEscalated records that we've added (address, attr) pairs to
// lifecycle.ignore_changes so a re-occurrence on a subsequent iteration
// can be detected and surfaced as a real failure.
func markEscalated(cur map[string][]string, dst map[string]map[string]struct{}) {
	for addr, attrs := range cur {
		if dst[addr] == nil {
			dst[addr] = map[string]struct{}{}
		}
		for _, a := range attrs {
			dst[addr][a] = struct{}{}
		}
	}
}

func driftAttrCount(drift map[string][]string) int {
	var n int
	for _, attrs := range drift {
		n += len(attrs)
	}
	return n
}

// progressf writes a human-readable progress line to w when w is non-nil.
// Best-effort: progress sink failures must never affect drift convergence.
func progressf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

// classificationFatal returns the first fatal condition (delete or
// replace) encountered in classifications. nil otherwise.
func classificationFatal(cs []driftClassification) error {
	for _, c := range cs {
		if c.mustDelete {
			return fmt.Errorf("resource %q is marked for delete in plan; an import-only run must not produce deletes", c.address)
		}
		if c.mustReplace {
			return fmt.Errorf("resource %q must be replaced (reason: %s); driftfix will not auto-resolve replaces — operator must reconcile manually", c.address, c.replaceWhy)
		}
	}
	return nil
}

// driftMap collapses classifications into a {address: sorted-attr-names}
// shape so the stability detector can DeepEqual two iterations.
func driftMap(cs []driftClassification) map[string][]string {
	out := make(map[string][]string, len(cs))
	for _, c := range cs {
		if len(c.driftAttrs) == 0 {
			continue
		}
		out[c.address] = append([]string(nil), c.driftAttrs...)
	}
	return out
}

// errStableUnresolved formats the same-drift-as-last-iteration message
// with enough detail that the operator knows which attr to look at.
func errStableUnresolved(drift map[string][]string) error {
	var b []byte
	b = append(b, "drift stable but unresolved across iterations:\n"...)
	for addr, attrs := range drift {
		b = append(b, "  "...)
		b = append(b, addr...)
		b = append(b, ": "...)
		b = append(b, fmt.Sprintf("%v", attrs)...)
		b = append(b, '\n')
	}
	return errors.New(string(b))
}
