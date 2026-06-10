package genconfig

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlanGenerateOpts is the mutation-resistant guard for the genconfig
// readback parallelism (luthersystems/ui-core#420): the genconfig
// `terraform plan -generate-config-out` phase was ≈91% of a measured
// 1h41m / 481-resource reverse import, run at terraform's default
// -parallelism=10. planGenerateOpts MUST pass tfexec.Parallelism alongside
// the generate-config-out target. If anyone drops the parallelism option from
// THIS path specifically, this test fails — even though a real terraform run
// would still "succeed" (just slowly), so no other test would catch the
// regression.
func TestPlanGenerateOpts(t *testing.T) {
	t.Parallel()

	const generated = "/tmp/generated.tf"
	const parallelism = 25
	opts := planGenerateOpts(generated, parallelism)

	// The generate-config-out target must still be present.
	require.Contains(t, opts, tfexec.GenerateConfigOut(generated),
		"genconfig readback must keep its -generate-config-out target")

	// The parallelism option must be present AND carry the exact value
	// threaded in. reflect.DeepEqual compares the (unexported) parallelism
	// field, so a value mutation (e.g. silently forcing 10) also fails here.
	var foundParallelism bool
	for _, o := range opts {
		if reflect.DeepEqual(o, tfexec.Parallelism(parallelism)) {
			foundParallelism = true
		}
	}
	require.True(t, foundParallelism,
		"genconfig readback must pass tfexec.Parallelism(%d); dropping -parallelism re-introduces the ui-core#420 default-10 bottleneck", parallelism)
}

// TestDefaultGenconfigParallelism pins the chosen default so a casual edit
// back to terraform's default (10) — which would silently undo the
// ui-core#420 fix — trips the suite. 25 is the value the ticket sized for the
// readback; see DefaultGenconfigParallelism for the throttle-safety rationale.
func TestDefaultGenconfigParallelism(t *testing.T) {
	t.Parallel()
	require.Equal(t, 25, DefaultGenconfigParallelism)
}

// TestOptionsParallelismOrDefault pins that the unset (<= 0) zero value falls
// back to the default and an explicit positive value is honored.
func TestOptionsParallelismOrDefault(t *testing.T) {
	t.Parallel()
	assert.Equal(t, DefaultGenconfigParallelism, Options{}.parallelismOrDefault(), "zero value defaults")
	assert.Equal(t, DefaultGenconfigParallelism, Options{Parallelism: -3}.parallelismOrDefault(), "negative defaults")
	assert.Equal(t, 40, Options{Parallelism: 40}.parallelismOrDefault(), "explicit positive honored")
}

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
