package genconfig

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// terraformRunner is the narrow surface genconfig needs from terraform-exec.
// Tests inject a fakeRunner; production uses execRunner backed by a real
// terraform binary on PATH.
type terraformRunner interface {
	// Version runs `terraform version`. genconfig calls it as a one-time,
	// serial warm-up before the multi-region fan-out: the first `terraform`
	// invocation in an environment whose pinned version isn't pre-baked makes
	// tfenv auto-install it (download → unzip → chmod +x → exec), which is NOT
	// concurrency-safe — N regions racing that first install hit
	// `Permission denied` / exit 126 on the freshly written, not-yet-executable
	// binary (#724). Running version once forces that install serially.
	Version(ctx context.Context) error
	Init(ctx context.Context) error
	// PlanGenerate runs `terraform plan -generate-config-out=<path>
	// -parallelism=<parallelism>` and returns whether the plan has changes
	// (which it always does for a fresh import — generate-config-out only
	// fires when there's something to generate). parallelism is the resolved
	// (already defaulted) value the readback should refresh resources at; see
	// genconfig.DefaultGenconfigParallelism.
	PlanGenerate(ctx context.Context, generatedPath string, parallelism int) (changes bool, err error)
	Validate(ctx context.Context) error
	ProvidersSchema(ctx context.Context) (*tfjson.ProviderSchemas, error)
}

// execRunner adapts a *tfexec.Terraform to the terraformRunner interface.
// One execRunner instance per workdir; constructing one shells out to
// `terraform version` only on first use through the underlying library.
type execRunner struct {
	tf *tfexec.Terraform
}

// newExecRunner constructs an execRunner for workdir. When stream is
// non-nil the terraform subprocess streams its *stderr* there so a
// long-running caller can surface live progress (terraform's human
// progress lines and the "Config generation is experimental" warning from
// plan -generate-config-out both land on stderr); nil keeps the historical
// "discard subprocess output" behavior.
//
// We deliberately do NOT call tf.SetStdout: the JSON-capture commands
// (ProvidersSchema, Validate) write their giant `-json` payload to stdout,
// and terraform-exec merges tf.stdout into the captured stream
// (runTerraformCmdJSON → mergeWriters(cmd.Stdout, tf.stdout)). Pointing
// tf.stdout at the live log therefore dumps the ~19MB provider schema /
// validate JSON into the user-facing stream and blows the gRPC-limited log
// (reliable#1896). Leaving stdout unset lets tfexec discard it for these
// commands while the JSON is still captured internally — only the leak
// stops. This mirrors pkg/reverseimport/terraform.go, which streams only
// stderr for its `-json` capture commands.
func newExecRunner(workdir string, stream io.Writer) (*execRunner, error) {
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return nil, fmt.Errorf("terraform binary not found on PATH: %w", err)
	}
	tf, err := tfexec.NewTerraform(workdir, bin)
	if err != nil {
		return nil, fmt.Errorf("init terraform-exec: %w", err)
	}
	if stream != nil {
		tf.SetStderr(stream)
	}
	return &execRunner{tf: tf}, nil
}

// Version runs `terraform version`, discarding the parsed result — genconfig
// only needs the side effect: forcing the binary to actually execute so tfenv
// resolves and installs the pinned version. skipCache=true guarantees the
// binary runs (rather than returning a cached value) so a cold tfenv version
// is installed here, serially, before the parallel Init() fan-out.
func (r *execRunner) Version(ctx context.Context) error {
	_, _, err := r.tf.Version(ctx, true)
	return err
}

func (r *execRunner) Init(ctx context.Context) error {
	return r.tf.Init(ctx, tfexec.Upgrade(false))
}

func (r *execRunner) PlanGenerate(ctx context.Context, generatedPath string, parallelism int) (bool, error) {
	return r.tf.Plan(ctx, planGenerateOpts(generatedPath, parallelism)...)
}

// planGenerateOpts is the pure builder for the genconfig readback's tfexec
// plan options. Factored out so a unit test can assert the genconfig path
// always passes `-parallelism` (tfexec.Parallelism) alongside the
// generate-config-out target without shelling out to a real terraform binary
// — the mutation-resistant guard for luthersystems/ui-core#420: if someone
// drops the parallelism option from this path, TestPlanGenerateOpts fails.
func planGenerateOpts(generatedPath string, parallelism int) []tfexec.PlanOption {
	return []tfexec.PlanOption{
		tfexec.GenerateConfigOut(generatedPath),
		tfexec.Parallelism(parallelism),
	}
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
