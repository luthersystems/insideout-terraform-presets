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

	runner := opts.Runner
	if runner == nil {
		r, err := newExecRunner(opts.Workdir, opts.Stdout)
		if err != nil {
			return nil, err
		}
		runner = r
	}

	generatedPath := filepath.Join(opts.Workdir, generatedFile)
	planPath := filepath.Join(opts.Workdir, planFile)

	var prevDrift map[string][]string
	// alreadyEscalated tracks (address, attr) pairs we've already moved
	// to lifecycle.ignore_changes. If the same drift recurs AFTER
	// escalation, it's truly stable-unresolved and we surface it as
	// fatal — the operator must inspect manually.
	alreadyEscalated := map[string]map[string]struct{}{}

	for iter := 1; iter <= opts.MaxIterations; iter++ {
		hasChanges, err := runner.PlanTo(ctx, planPath)
		if err != nil {
			return nil, fmt.Errorf("driftfix iter %d: terraform plan: %w", iter, err)
		}
		if !hasChanges {
			return &Result{GeneratedPath: generatedPath, Iterations: iter}, nil
		}

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
			patched, err := applyIgnoreChangesEscalation(raw, classifications)
			if err != nil {
				return nil, fmt.Errorf("driftfix iter %d: ignore_changes escalation: %w", iter, err)
			}
			if err := os.WriteFile(generatedPath, patched, 0o644); err != nil {
				return nil, fmt.Errorf("driftfix iter %d: write generated.tf: %w", iter, err)
			}
			markEscalated(curDrift, alreadyEscalated)
			if err := runner.Validate(ctx); err != nil {
				return nil, fmt.Errorf("driftfix iter %d: validate after ignore_changes: %w", iter, err)
			}
			prevDrift = curDrift
			continue
		}
		prevDrift = curDrift

		patched, err := applyDriftPatches(raw, classifications)
		if err != nil {
			return nil, fmt.Errorf("driftfix iter %d: patch: %w", iter, err)
		}
		if err := os.WriteFile(generatedPath, patched, 0o644); err != nil {
			return nil, fmt.Errorf("driftfix iter %d: write generated.tf: %w", iter, err)
		}
		if err := runner.Validate(ctx); err != nil {
			return nil, fmt.Errorf("driftfix iter %d: validate after patch: %w", iter, err)
		}
	}
	return nil, fmt.Errorf("driftfix: %d iterations exhausted without convergence", opts.MaxIterations)
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
