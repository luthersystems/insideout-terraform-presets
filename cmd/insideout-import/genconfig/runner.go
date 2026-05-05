package genconfig

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// terraformRunner is the narrow surface genconfig needs from terraform-exec.
// Tests inject a fakeRunner; production uses execRunner backed by a real
// terraform binary on PATH.
type terraformRunner interface {
	Init(ctx context.Context) error
	// PlanGenerate runs `terraform plan -generate-config-out=<path>` and
	// returns whether the plan has changes (which it always does for a fresh
	// import — generate-config-out only fires when there's something to
	// generate).
	PlanGenerate(ctx context.Context, generatedPath string) (changes bool, err error)
	Validate(ctx context.Context) error
	ProvidersSchema(ctx context.Context) (*tfjson.ProviderSchemas, error)
}

// execRunner adapts a *tfexec.Terraform to the terraformRunner interface.
// One execRunner instance per workdir; constructing one shells out to
// `terraform version` only on first use through the underlying library.
type execRunner struct {
	tf *tfexec.Terraform
}

func newExecRunner(workdir string) (*execRunner, error) {
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return nil, fmt.Errorf("terraform binary not found on PATH: %w", err)
	}
	tf, err := tfexec.NewTerraform(workdir, bin)
	if err != nil {
		return nil, fmt.Errorf("init terraform-exec: %w", err)
	}
	return &execRunner{tf: tf}, nil
}

func (r *execRunner) Init(ctx context.Context) error {
	return r.tf.Init(ctx, tfexec.Upgrade(false))
}

func (r *execRunner) PlanGenerate(ctx context.Context, generatedPath string) (bool, error) {
	return r.tf.Plan(ctx, tfexec.GenerateConfigOut(generatedPath))
}

func (r *execRunner) Validate(ctx context.Context) error {
	out, err := r.tf.Validate(ctx)
	if err != nil {
		return err
	}
	if !out.Valid {
		return fmt.Errorf("terraform validate reported %d error diagnostic(s)", out.ErrorCount)
	}
	return nil
}

func (r *execRunner) ProvidersSchema(ctx context.Context) (*tfjson.ProviderSchemas, error) {
	return r.tf.ProvidersSchema(ctx)
}
