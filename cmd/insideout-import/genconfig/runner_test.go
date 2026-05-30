package genconfig

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// builtinProviderStack is a terraform config that only uses the builtin
// terraform_data resource (terraform.io/builtin/terraform). It needs no plugin
// download and no cloud credentials, so `terraform init` succeeds fully
// offline — yet `terraform providers schema -json` still emits a real,
// non-trivial provider schema. That makes it a cheap, deterministic fixture
// for asserting the JSON-capture commands don't leak their payload into the
// streamed log.
const builtinProviderStack = `
resource "terraform_data" "x" {
  input = "hello"
}
`

// TestExecRunner_JSONCaptureDoesNotLeakToStream is the regression guard for
// reliable#1896: the reverse-import genconfig phase dumped ~19MB of
// `terraform providers schema -json` (and `terraform validate -json`) into
// the user-facing live log because newExecRunner pointed tf.stdout at the
// stream. terraform-exec merges tf.stdout into the JSON-capture stdout
// (runTerraformCmdJSON → mergeWriters(cmd.Stdout, tf.stdout)), so the giant
// schema JSON leaked.
//
// The fix streams only stderr. This test runs the real JSON-capture path
// against an offline builtin-provider stack and asserts:
//   - ProvidersSchema / Validate still return a populated, parsed result, and
//   - the streamed writer contains NONE of the JSON markers that the leak
//     would have dumped into it.
func TestExecRunner_JSONCaptureDoesNotLeakToStream(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform CLI not available, skipping JSON-capture leak test")
	}

	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "main.tf"), []byte(builtinProviderStack), 0o644))

	// streamCapturingWriter stands in for the Mars live-log sink. Anything the
	// runner streams here would, in the buggy version, contain the schema JSON.
	var stream stringSink
	runner, err := newExecRunner(workdir, &stream)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, runner.Init(ctx), "terraform init (builtin provider, offline)")

	// ProvidersSchema captures ~3KB of `-json` on stdout internally. The
	// builtin terraform provider always yields a non-empty schema set.
	schemas, err := runner.ProvidersSchema(ctx)
	require.NoError(t, err)
	require.NotNil(t, schemas)
	assert.NotEmpty(t, schemas.Schemas, "parsed provider schema should be populated")

	// Validate captures `validate -json` on stdout internally.
	require.NoError(t, runner.Validate(ctx), "builtin-provider stack validates clean")

	// The core assertion: none of the JSON-capture payload reached the stream.
	// "provider_schemas" / "nesting_mode" are providers-schema-only markers;
	// "format_version" appears in both schema and validate JSON. If tf.stdout
	// were wired to the stream (the bug), all three would be present.
	streamed := stream.String()
	for _, marker := range []string{`"provider_schemas"`, `"nesting_mode"`, `"format_version"`} {
		assert.NotContainsf(t, streamed, marker,
			"JSON-capture payload leaked to the streamed log (marker %q); reliable#1896 regression", marker)
	}
}

// stringSink is a small mutex-guarded io.Writer for capturing streamed
// output in tests. terraform-exec drains stderr from a goroutine that runs
// concurrently with the command, so the writer must be safe under -race.
type stringSink struct {
	mu  sync.Mutex
	buf []byte
}

func (s *stringSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	return len(p), nil
}

func (s *stringSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.buf)
}
