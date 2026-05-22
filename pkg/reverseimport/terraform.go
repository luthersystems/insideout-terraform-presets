package reverseimport

import (
	"context"
	"errors"
	"fmt"
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
}

func (r execTerraformRunner) bin() string {
	if r.binary != "" {
		return r.binary
	}
	return "terraform"
}

func (r execTerraformRunner) Init(ctx context.Context, dir string) error {
	return r.run(ctx, dir, "init", "-input=false", "-no-color")
}

func (r execTerraformRunner) Validate(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.bin(), "validate", "-json")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	out, err := cmd.Output()
	if err != nil {
		return out, err
	}
	return out, nil
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
	cmd.Stderr = os.Stderr
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", r.bin(), args, err)
	}
	return nil
}
