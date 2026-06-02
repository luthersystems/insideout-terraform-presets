package driftfix

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

// builtinProviderStack uses only the builtin terraform_data resource
// (terraform.io/builtin/terraform), so `terraform init` and `plan` both
// succeed fully offline with no plugin download or cloud credentials — yet the
// JSON-capture commands (validate -json, show -json) still emit real payloads.
// A deterministic fixture for asserting those payloads don't leak into the
// streamed log.
const builtinProviderStack = `
resource "terraform_data" "x" {
  input = "hello"
}
`

// TestExecRunner_JSONCaptureDoesNotLeakToStream is the driftfix-side
// regression guard for reliable#1896. Like the genconfig runner, the driftfix
// execRunner used to point tf.stdout at the live-log stream, so the
// JSON-capture commands (Validate, ShowPlan) leaked their `-json` payload into
// the user-facing log. The fix streams only stderr. This runs the real
// Validate + Plan/Show path against an offline builtin-provider stack and
// asserts the parsed results are populated while the streamed writer contains
// none of the JSON-capture markers.
func TestExecRunner_JSONCaptureDoesNotLeakToStream(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform CLI not available, skipping JSON-capture leak test")
	}

	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "main.tf"), []byte(builtinProviderStack), 0o644))

	// Driftfix runs after genconfig has already done init, so the runner has
	// no Init method. Init the workdir externally to mirror that contract.
	ctx := context.Background()
	initCmd := exec.CommandContext(ctx, "terraform", "init", "-input=false", "-no-color")
	initCmd.Dir = workdir
	initCmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	out, err := initCmd.CombinedOutput()
	require.NoError(t, err, "terraform init (builtin provider, offline):\n%s", out)

	var stream stringSink
	runner, err := newExecRunner(workdir, &stream)
	require.NoError(t, err)

	// Validate captures `validate -json` on stdout internally.
	require.NoError(t, runner.Validate(ctx), "builtin-provider stack validates clean")

	// PlanTo writes a binary plan; ShowPlan decodes it via `show -json`, which
	// captures its JSON payload on stdout internally.
	planFile := filepath.Join(workdir, "tfplan.bin")
	_, err = runner.PlanTo(ctx, planFile)
	require.NoError(t, err)
	plan, err := runner.ShowPlan(ctx, planFile)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.NotEmpty(t, plan.FormatVersion, "parsed plan should carry a format version")

	// None of the JSON-capture payload should have reached the stream.
	// "format_version" appears in both validate and show JSON; "resource_changes"
	// is a show-plan marker. If tf.stdout were wired to the stream (the bug),
	// these would be present.
	streamed := stream.String()
	for _, marker := range []string{`"format_version"`, `"resource_changes"`, `"planned_values"`} {
		assert.NotContainsf(t, streamed, marker,
			"JSON-capture payload leaked to the streamed log (marker %q); reliable#1896 regression", marker)
	}
}

// TestExecRunner_PlanStreamsHumanDiffToStream is the positive companion to the
// #1896 leak guard above: the per-iteration `terraform plan` HUMAN diff MUST
// reach the live-log stream so the import plan-log console shows it. Before
// this change the driftfix runner left tf.stdout unset, so only driftfix's own
// "running terraform plan…" narration streamed and the actual plan output was
// invisible. PlanTo now scopes tf.stdout to the plan call (Show/Validate keep
// it discarded — asserted by the sibling test) so the human diff streams while
// the JSON-capture payloads stay internal.
func TestExecRunner_PlanStreamsHumanDiffToStream(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform CLI not available, skipping plan-stream test")
	}

	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "main.tf"), []byte(builtinProviderStack), 0o644))

	ctx := context.Background()
	initCmd := exec.CommandContext(ctx, "terraform", "init", "-input=false", "-no-color")
	initCmd.Dir = workdir
	initCmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	out, err := initCmd.CombinedOutput()
	require.NoError(t, err, "terraform init (builtin provider, offline):\n%s", out)

	var stream stringSink
	runner, err := newExecRunner(workdir, &stream)
	require.NoError(t, err)

	planFile := filepath.Join(workdir, "tfplan.bin")
	hasChanges, err := runner.PlanTo(ctx, planFile)
	require.NoError(t, err)
	assert.True(t, hasChanges, "creating terraform_data.x is a change")

	streamed := stream.String()
	// The human-readable plan diff (not JSON) must reach the live log.
	assert.Contains(t, streamed, "terraform_data.x", "plan human diff should stream to the log")
	assert.Contains(t, streamed, "Plan:", "plan summary should stream to the log")
	// Scoping still holds: the plan call must not drag show/validate JSON in.
	assert.NotContains(t, streamed, `"format_version"`,
		"plan stream must not carry JSON-capture payload (reliable#1896)")
}

// stringSink is a small mutex-guarded io.Writer for capturing streamed output
// in tests. terraform-exec drains stderr from a goroutine that runs
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
