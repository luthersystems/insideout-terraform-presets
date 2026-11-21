package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tree(files map[string]string) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range files {
		out[k] = []byte(v)
	}
	return out
}

// The v0.15.3 release (#958) is the case that started this: two modules
// gained variables, and the release notes had to say so by hand.
func TestModuleChangesListsInterfaceChanges(t *testing.T) {
	oldTree := tree(map[string]string{
		"aws/rds/variables.tf": `
variable "project" {}
variable "enable_logs" {
  type    = bool
  default = false
}
variable "gone" { default = 1 }
`,
		"aws/rds/outputs.tf": `output "endpoint" { value = 1 }
output "old_out" { value = 2 }`,
		"aws/old/main.tf":  `variable "x" {}`,
		"aws/same/main.tf": `variable "x" { default = "a" }`,
	})
	newTree := tree(map[string]string{
		"aws/rds/variables.tf": `
variable "project" {}
variable "enable_logs" {
  type = bool
  default = true
}
variable "retention" {
  default = 30
}
variable "needed" {}
`,
		"aws/rds/outputs.tf": `output "endpoint" { value = 1 }
output "arn" { value = 3 }`,
		"aws/new/main.tf":             `variable "x" {}`,
		"aws/same/main.tf":            `variable "x" { default   =   "a" }`,
		"aws/same/tests/a.tftest.hcl": `run "x" {}`,
	})
	changed := []string{
		"aws/rds/variables.tf", "aws/rds/outputs.tf", "aws/old/main.tf",
		"aws/new/main.tf", "aws/same/main.tf", "aws/same/tests/a.tftest.hcl",
		"pkg/planredact/redact.go", "pkg/planredact/redact_test.go", "README.md",
	}

	changes, err := moduleChanges(changed, oldTree, newTree)
	require.NoError(t, err)

	byDir := map[string]moduleChange{}
	for _, c := range changes {
		byDir[c.Dir] = c
	}
	require.Len(t, byDir, 4)

	rds := byDir["aws/rds"]
	assert.Equal(t, statusChanged, rds.Status)
	assert.Equal(t, []string{"needed", "retention"}, names(rds.VarsAdded))
	assert.Equal(t, []string{"gone"}, rds.VarsRemoved)
	assert.Equal(t, []defaultChange{{Name: "enable_logs", From: "false", To: "true"}}, rds.DefaultsChanged)
	assert.Equal(t, []string{"arn"}, rds.OutputsAdded)
	assert.Equal(t, []string{"old_out"}, rds.OutputsRemoved)
	assert.True(t, rds.breaking(), "a removed variable, a removed output and a new required variable break callers")

	assert.Equal(t, statusRemoved, byDir["aws/old"].Status)
	assert.Equal(t, statusNew, byDir["aws/new"].Status)
	// Only spacing changed in the default: that is not a default change.
	same := byDir["aws/same"]
	assert.Equal(t, statusChanged, same.Status)
	assert.Empty(t, same.DefaultsChanged)
	assert.False(t, same.breaking())
}

func TestPackagesSkipTestsAndKeepOrder(t *testing.T) {
	changed := []string{"pkg/b/x.go", "pkg/a/x_test.go", "pkg/a/sub/y.go", "pkg/c/z_test.go", "main.go"}
	assert.Equal(t, []string{"pkg/a", "pkg/b"}, changedPackages(changed))
}

func TestRenderMarksBreakingAndLinksPRs(t *testing.T) {
	out := render(
		[]string{"- Own the log groups (#958)"},
		[]moduleChange{
			{Dir: "aws/rds", Status: statusChanged, VarsAdded: []variable{{Name: "retention", Default: "30", HasDefault: true}}},
			{Dir: "aws/old", Status: statusRemoved},
		},
		[]string{"pkg/planredact"},
		"https://github.com/o/r/compare/v0.15.2...abc",
	)
	assert.Contains(t, out, "- Own the log groups (#958)")
	assert.Contains(t, out, "| `aws/rds` | Variables added: `retention` (default `30`) |")
	assert.Contains(t, out, "**Breaking:** `aws/old`")
	assert.Contains(t, out, "`pkg/planredact`")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(out), "https://github.com/o/r/compare/v0.15.2...abc"))
}

func TestPRLine(t *testing.T) {
	assert.Equal(t, "- Own the log groups (#958)", prLine("abcdef1234", "Merge pull request #958 from luthersystems/x", "Own the log groups\n\nbody"))
	assert.Equal(t, "- Fix a typo (abcdef1)", prLine("abcdef1234", "Fix a typo", ""))
}

func names(vs []variable) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}
