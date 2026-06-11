package reverseimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecTerraformRunnerValidateStreamsHumanOutputAndCapturesJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := writeFakeTerraform(t, dir, `#!/bin/sh
if [ "$1" = "validate" ] && [ "$2" = "-no-color" ]; then
  echo "human validate stdout"
  echo "human validate stderr" >&2
  exit 0
fi
if [ "$1" = "validate" ] && [ "$2" = "-json" ]; then
  echo "json validate stderr" >&2
  printf '{"valid":true,"diagnostics":[]}\n'
  exit 0
fi
echo "unexpected args: $*" >&2
exit 7
`)

	var stream strings.Builder
	out, err := execTerraformRunner{binary: bin, stdout: &stream}.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	gotStream := stream.String()
	for _, want := range []string{"human validate stdout", "human validate stderr", "json validate stderr"} {
		if !strings.Contains(gotStream, want) {
			t.Fatalf("stream missing %q:\n%s", want, gotStream)
		}
	}
	if strings.Contains(gotStream, `"valid":true`) {
		t.Fatalf("validate -json stdout leaked into stream:\n%s", gotStream)
	}
	if got := strings.TrimSpace(string(out)); got != `{"valid":true,"diagnostics":[]}` {
		t.Fatalf("validate JSON = %q", got)
	}
}

func TestExecTerraformRunnerValidateStillCapturesJSONAfterHumanValidateFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := writeFakeTerraform(t, dir, `#!/bin/sh
if [ "$1" = "validate" ] && [ "$2" = "-no-color" ]; then
  echo "Error: Incorrect attribute value type"
  echo "  with aws_route_table.rtb_03988a8cc66567bd1,"
  echo "attribute \"odb_network_arn\" is required"
  exit 1
fi
if [ "$1" = "validate" ] && [ "$2" = "-json" ]; then
  printf '{"valid":false,"error_count":1,"diagnostics":[{"summary":"Incorrect attribute value type"}]}\n'
  exit 0
fi
echo "unexpected args: $*" >&2
exit 7
`)

	var stream strings.Builder
	out, err := execTerraformRunner{binary: bin, stdout: &stream}.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("Validate err = nil, want human validate failure")
	}
	gotStream := stream.String()
	for _, want := range []string{
		"Error: Incorrect attribute value type",
		"aws_route_table.rtb_03988a8cc66567bd1",
		`attribute "odb_network_arn" is required`,
	} {
		if !strings.Contains(gotStream, want) {
			t.Fatalf("stream missing %q:\n%s", want, gotStream)
		}
	}
	if got := strings.TrimSpace(string(out)); !strings.Contains(got, `"valid":false`) {
		t.Fatalf("validate JSON was not captured after failure: %q", got)
	}
}

// TestPlanArgs is the mutation-resistant guard for the final-plan parallelism
// (luthersystems/ui-core#420): planArgs MUST carry -parallelism=<n> and keep
// -detailed-exitcode (so an import-only diff isn't read as a failure).
func TestPlanArgs(t *testing.T) {
	t.Parallel()
	args := planArgs("/tmp/tfplan.bin", 25)
	joined := strings.Join(args, " ")
	for _, want := range []string{"plan", "-detailed-exitcode", "-parallelism=25", "-out=/tmp/tfplan.bin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("planArgs missing %q: %v", want, args)
		}
	}
}

// TestPlanParallelismDefaults pins that a zero/unset runner parallelism falls
// back to DefaultParallelism (25) so the engine's final plan never silently
// drops back to terraform's default of 10.
func TestPlanParallelismDefaults(t *testing.T) {
	t.Parallel()
	if got := (execTerraformRunner{}).planParallelism(); got != DefaultParallelism {
		t.Fatalf("zero-value runner planParallelism = %d, want %d", got, DefaultParallelism)
	}
	if got := (execTerraformRunner{parallelism: -2}).planParallelism(); got != DefaultParallelism {
		t.Fatalf("negative runner planParallelism = %d, want %d", got, DefaultParallelism)
	}
	if got := (execTerraformRunner{parallelism: 33}).planParallelism(); got != 33 {
		t.Fatalf("explicit runner planParallelism = %d, want 33", got)
	}
	if DefaultParallelism != 25 {
		t.Fatalf("DefaultParallelism = %d, want 25", DefaultParallelism)
	}
}

// TestExecTerraformRunnerPlanPassesParallelism drives the real Plan path
// against a fake terraform that records its argv, proving the engine's final
// plan actually shells out with -parallelism. End-to-end complement to
// TestPlanArgs.
func TestExecTerraformRunnerPlanPassesParallelism(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	bin := writeFakeTerraform(t, dir, `#!/bin/sh
echo "$@" > `+argsFile+`
exit 0
`)
	planPath := filepath.Join(dir, "tfplan.bin")
	err := execTerraformRunner{binary: bin, parallelism: 25}.Plan(context.Background(), dir, planPath)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	recorded, readErr := os.ReadFile(argsFile)
	if readErr != nil {
		t.Fatalf("read recorded args: %v", readErr)
	}
	if !strings.Contains(string(recorded), "-parallelism=25") {
		t.Fatalf("terraform plan argv missing -parallelism=25: %q", string(recorded))
	}
}

func writeFakeTerraform(t *testing.T, dir, script string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
