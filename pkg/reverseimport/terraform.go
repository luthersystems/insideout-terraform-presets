package reverseimport

import (
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
	err := r.run(ctx, dir, "plan", "-input=false", "-no-color", "-detailed-exitcode", "-out="+planPath)
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 2 {
		return nil
	}
	return err
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
