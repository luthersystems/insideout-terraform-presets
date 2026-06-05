package reverseimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type terraformRunner interface {
	Init(ctx context.Context, dir string) error
	Validate(ctx context.Context, dir string) ([]byte, error)
	Plan(ctx context.Context, dir, planPath string) error
	ShowPlanJSON(ctx context.Context, dir, planPath string) ([]byte, error)
}

// planOutputError carries the captured terraform stderr alongside the
// underlying exec failure so the partial-tolerance attribution layer can
// scan the diagnostic text for a resource address (`terraform plan` does not
// emit machine-readable per-resource diagnostics the way `validate -json`
// does — the only signal is the human-readable error block). A fake runner in
// tests can return this type directly to exercise plan-error attribution.
//
// stderrText returns the captured terraform stderr for any error that
// implements it, so attribution works uniformly across the real runner and
// test doubles. An error that does not implement the interface yields "" —
// the attribution layer then treats the plan failure as un-attributable and
// aborts (preserving the existing systemic-error safety).
type planOutputError struct {
	output string
	err    error
}

func (e *planOutputError) Error() string {
	if e.output == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.output
}

func (e *planOutputError) Unwrap() error { return e.err }

func (e *planOutputError) stderrText() string { return e.output }

// stderrTexter is implemented by errors that carry captured terraform stderr.
type stderrTexter interface{ stderrText() string }

// planStderr extracts captured terraform stderr from err when present.
func planStderr(err error) string {
	var t stderrTexter
	if errors.As(err, &t) {
		return t.stderrText()
	}
	return ""
}

type execTerraformRunner struct {
	binary string
	// stdout receives the live stdout of the streaming commands (init,
	// plan) and the stderr of every command. nil falls back to os.Stdout
	// / os.Stderr so a zero-value runner keeps the historical behavior.
	stdout io.Writer
}

func (r execTerraformRunner) bin() string {
	if r.binary != "" {
		return r.binary
	}
	return "terraform"
}

// outW is the destination for streamed stdout/stderr. nil falls back to
// the process stderr so a zero-value runner (and the historical
// os.Stdout/os.Stderr behavior) still surfaces output somewhere.
func (r execTerraformRunner) outW() io.Writer {
	if r.stdout != nil {
		return r.stdout
	}
	return os.Stderr
}

func (r execTerraformRunner) Init(ctx context.Context, dir string) error {
	return r.run(ctx, dir, "init", "-input=false", "-no-color")
}

func (r execTerraformRunner) Validate(ctx context.Context, dir string) ([]byte, error) {
	streamErr := r.run(ctx, dir, "validate", "-no-color")
	cmd := exec.CommandContext(ctx, r.bin(), "validate", "-json")
	cmd.Dir = dir
	// stdout is captured as the validate.json artifact, so only stderr
	// streams to the progress sink here.
	cmd.Stderr = r.outW()
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	out, err := cmd.Output()
	if err != nil {
		return out, err
	}
	return out, streamErr
}

func (r execTerraformRunner) Plan(ctx context.Context, dir, planPath string) error {
	// Tee stderr into a buffer so a plan failure can be attributed to a
	// specific resource address by the partial-tolerance loop. The live
	// sink still receives the same bytes (the import wizard's log console
	// keeps streaming); the buffer is only read on error.
	var captured bytes.Buffer
	cmd := exec.CommandContext(ctx, r.bin(), "plan", "-input=false", "-no-color", "-detailed-exitcode", "-out="+planPath)
	cmd.Dir = dir
	cmd.Stdout = r.outW()
	cmd.Stderr = io.MultiWriter(r.outW(), &captured)
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 2 {
		// -detailed-exitcode: 2 means "succeeded, with a non-empty diff"
		// (the import-only plan). Not a failure.
		return nil
	}
	return &planOutputError{output: captured.String(), err: fmt.Errorf("%s plan: %w", r.bin(), err)}
}

func (r execTerraformRunner) ShowPlanJSON(ctx context.Context, dir, planPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.bin(), "show", "-json", planPath)
	cmd.Dir = dir
	// stdout is captured as the tfplan.json artifact, so only stderr
	// streams to the progress sink here.
	cmd.Stderr = r.outW()
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r execTerraformRunner) run(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, r.bin(), args...)
	cmd.Dir = dir
	cmd.Stdout = r.outW()
	cmd.Stderr = r.outW()
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", r.bin(), args, err)
	}
	return nil
}
