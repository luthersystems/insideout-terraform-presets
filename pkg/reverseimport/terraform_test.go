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

func writeFakeTerraform(t *testing.T, dir, script string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
