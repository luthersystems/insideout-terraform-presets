package driftfix

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// terraformRunner is the narrow surface driftfix needs from terraform-exec.
// Tests inject a fake; production uses execRunner backed by the
// `terraform` binary on PATH.
//
// Drift fix runs after genconfig.Run has already done init + plan. We
// don't re-init here — re-init would clobber the .terraform dir genconfig
// just produced. The runner's only job is to drive plan-and-show cycles
// and re-validate after each patch.
type terraformRunner interface {
	// PlanTo runs `terraform plan -out=<planFile>`. Returns hasChanges =
	// true iff the plan contains a non-no-op resource change.
	PlanTo(ctx context.Context, planFile string) (hasChanges bool, err error)
	// ShowPlan decodes a binary plan file into the typed tfjson.Plan
	// shape so the patch pass can walk ResourceChanges.
	ShowPlan(ctx context.Context, planFile string) (*tfjson.Plan, error)
	// Validate re-runs `terraform validate` after a patch. We re-validate
	// every iteration because the patch can drop required attrs and the
	// loop must surface that as a fatal rather than march into another
	// plan call.
	Validate(ctx context.Context) error
}

// execRunner adapts a *tfexec.Terraform to the terraformRunner interface.
// The constructor is identical in shape to genconfig.execRunner — both
// ride the same workdir, but each package declares its own runner so
// the dependency direction stays one-way (driftfix never imports
// genconfig).
type execRunner struct {
	tf *tfexec.Terraform
	// stream, when non-nil, is the live progress sink. PlanTo points
	// tf.stdout here for the duration of the plan call so the
	// human-readable plan diff streams to the caller (the Mars
	// reverse-import job → import plan-log console). Show/Validate must NOT
	// stream — terraform-exec captures their -json payloads on stdout — so
	// PlanTo restores io.Discard before returning.
	stream io.Writer
}

// newExecRunner constructs an execRunner for workdir. When stream is
// non-nil the terraform subprocess streams its stderr there (always) and
// its stdout there for the plan call only (see PlanTo) so a long-running
// caller can surface the live plan diff; nil keeps the historical
// "discard subprocess output" behavior.
//
// stdout is scoped to PlanTo rather than set globally: ShowPlan
// (ShowPlanFile) and Validate capture `-json` payloads on stdout, and
// terraform-exec shares one tf.stdout across commands, so a global stdout
// would leak that JSON into the user-facing log and blow the gRPC-limited
// stream (reliable#1896). PlanTo restores io.Discard before returning so
// those commands keep their JSON captured internally. Mirrors the
// stderr-only discipline in genconfig.execRunner and the human-vs-json
// split in pkg/reverseimport/terraform.go.
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
	return &execRunner{tf: tf, stream: stream}, nil
}

func (r *execRunner) PlanTo(ctx context.Context, planFile string) (bool, error) {
	// Stream the human-readable `terraform plan` diff to the live log for
	// THIS call only. ShowPlan/Validate emit -json on stdout and
	// terraform-exec shares one stdout across commands, so restore
	// io.Discard before returning to keep those payloads captured
	// (reliable#1896). The plan text is monochrome (no TTY); the import
	// plan-log console colorizes it client-side (lib/stream/logColorize).
	if r.stream != nil {
		r.tf.SetStdout(r.stream)
		defer r.tf.SetStdout(io.Discard)
	}
	return r.tf.Plan(ctx, tfexec.Out(planFile))
}

func (r *execRunner) ShowPlan(ctx context.Context, planFile string) (*tfjson.Plan, error) {
	return r.tf.ShowPlanFile(ctx, planFile)
}

func (r *execRunner) Validate(ctx context.Context) error {
	out, err := r.tf.Validate(ctx)
	if err != nil {
		return err
	}
	if !out.Valid {
		msgs := make([]string, 0, len(out.Diagnostics))
		for _, d := range out.Diagnostics {
			if d.Severity != tfjson.DiagnosticSeverityError {
				continue
			}
			msg := d.Summary
			if d.Detail != "" {
				msg = msg + ": " + d.Detail
			}
			if d.Range != nil {
				msg = fmt.Sprintf("%s:%d: %s", d.Range.Filename, d.Range.Start.Line, msg)
			}
			msgs = append(msgs, msg)
		}
		return fmt.Errorf("terraform validate reported %d error diagnostic(s): %s", out.ErrorCount, strings.Join(msgs, "; "))
	}
	return nil
}
