package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitRepo(t *testing.T) func(args ...string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q", "-b", "main")
	return run
}

func write(t *testing.T, p, s string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, []byte(s), 0o600))
}

// Review of #961: a renamed module was listed only as new, and a major bump
// (the shared workflow passes no LAST_TAG) compared with an empty tree, so
// neither showed a Breaking line.
func TestRunRenameAndMajorBump(t *testing.T) {
	g := gitRepo(t)
	write(t, "aws/foo/variables.tf", `variable "x" { default = 1 }`)
	g("add", "-A")
	g("commit", "-q", "-m", "one")
	g("tag", "v0.15.3")
	g("mv", "aws/foo", "aws/bar")
	g("commit", "-q", "-m", "rename")

	for _, lastTag := range []string{"v0.15.3", ""} {
		out, err := run(lastTag)
		require.NoError(t, err)
		assert.Contains(t, out, "| `aws/foo` | Removed module |", lastTag)
		assert.Contains(t, out, "| `aws/bar` | New module |", lastTag)
		assert.Contains(t, out, "**Breaking:** `aws/foo`", lastTag)
	}
	out, err := run("")
	require.NoError(t, err)
	assert.Contains(t, out, "Compared with v0.15.3, the last release before this one.")
}

func TestRunFirstReleaseAndNoChange(t *testing.T) {
	g := gitRepo(t)
	write(t, "aws/foo/variables.tf", `variable "x" {}`)
	write(t, "README.md", "x")
	g("add", "-A")
	g("commit", "-q", "-m", "one")

	out, err := run("")
	require.NoError(t, err)
	assert.Contains(t, out, "| `aws/foo` | New module |")
	assert.NotContains(t, out, "Compared with")

	g("tag", "v0.1.0")
	write(t, "README.md", "y")
	g("commit", "-q", "-am", "docs")
	out, err = run("v0.1.0")
	require.NoError(t, err)
	assert.Equal(t, "", out, "no module or package change prints nothing")
}
