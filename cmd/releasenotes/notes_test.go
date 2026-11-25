package main

import (
	"strings"
	"testing"
	"unicode/utf8"

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
	changed := []string{"pkg/b/x.go", "pkg/a/x_test.go", "pkg/a/sub/y.go", "pkg/c/z_test.go", "zz_embed_test.go", "cmd/releasenotes/main.go"}
	assert.Equal(t, []string{"pkg/a", "pkg/b"}, changedPackages(changed))
	// zz_embed.go decides which preset files a consumer gets.
	assert.Equal(t, []string{rootPackage}, changedPackages([]string{"zz_embed.go"}))
}

func TestRenderMarksBreaking(t *testing.T) {
	out := render(
		[]moduleChange{
			{Dir: "aws/rds", Status: statusChanged, VarsAdded: []variable{{Name: "retention", Default: "30", HasDefault: true}}},
			{Dir: "aws/old", Status: statusRemoved},
		},
		[]string{"pkg/planredact"},
	)
	assert.True(t, strings.HasPrefix(out, "## Module Changes\n"))
	assert.Contains(t, out, "| `aws/rds` | Variables added: `retention` (default `30`) |")
	assert.Contains(t, out, "**Breaking:** `aws/old`")
	assert.Contains(t, out, "`pkg/planredact`")
	// The shared workflow writes the list of changes and the compare link.
	assert.NotContains(t, out, "What's Changed")
	assert.NotContains(t, out, "Full Changelog")
	assert.True(t, strings.HasSuffix(out, ".\n") && !strings.HasSuffix(out, "\n\n"))
}

// With no module or package change the section is left out, so the release
// has no empty heading.
func TestRenderEmptyPrintsNothing(t *testing.T) {
	assert.Equal(t, "", render(nil, nil))
	assert.Contains(t, render(nil, []string{"pkg/a"}), "Go packages changed: `pkg/a`.")
}

// Review of #961: text comparison flagged changes that do not change what a
// caller may pass: a reflowed object type, a reworded error_message, an
// explicit sensitive = false.
func TestModuleChangesIgnoreRewrites(t *testing.T) {
	oldTree := tree(map[string]string{"aws/x/variables.tf": `
variable "obj" { type = object({ a = string, b = number }) }
variable "env" {
  validation {
    condition     = length(var.env) > 0
    error_message = "env must be set"
  }
}
output "id" { value = 1 }
`})
	newTree := tree(map[string]string{"aws/x/variables.tf": `
variable "obj" {
  type = object({
    a = string
    b = number
  })
}
variable "env" {
  validation {
    # Say what to set.
    condition     = length(var.env) > 0
    error_message = "Set env."
  }
}
output "id" {
  value     = 1
  sensitive = false
}
`})
	changes, err := moduleChanges([]string{"aws/x/variables.tf"}, oldTree, newTree)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	c := changes[0]
	assert.Empty(t, c.TypesChanged)
	assert.Empty(t, c.ValidationsChanged)
	assert.Empty(t, c.SensitiveChanged)
	assert.False(t, c.breaking())
	assert.False(t, c.check())
}

func names(vs []variable) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

// Review of #961: the first version read only names and default text, so a
// type change shipped as "No variable or output change", and a reformatted
// list default showed as a new default.
func TestModuleChangesTypesValidationsAndFormatting(t *testing.T) {
	oldTree := tree(map[string]string{"aws/x/variables.tf": `
variable "count_of" { type = string }
variable "list" {
  default = [
    "a",
    "b",
  ]
}
variable "env" {
  default = "dev"
  validation {
    condition     = length(var.env) > 0
    error_message = "env"
  }
}
variable "maybe" { default = null }
output "secret" { value = 1 }
`})
	newTree := tree(map[string]string{"aws/x/variables.tf": `
variable "count_of" { type = number }
variable "list" { default = ["a", "b"] }
variable "env" {
  default = "dev"
  validation {
    condition     = contains(["dev", "prod"], var.env)
    error_message = "env"
  }
}
variable "maybe" {
  default  = null
  nullable = false
}
output "secret" {
  value     = 1
  sensitive = true
}
`})
	changes, err := moduleChanges([]string{"aws/x/variables.tf"}, oldTree, newTree)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	c := changes[0]
	assert.Empty(t, c.DefaultsChanged, "a reformatted list is the same default")
	assert.Equal(t, []string{"count_of"}, c.TypesChanged)
	assert.Equal(t, []string{"maybe"}, c.NullableOff)
	assert.Equal(t, []string{"env"}, c.ValidationsChanged)
	assert.Equal(t, []string{"secret"}, c.SensitiveChanged)
	assert.True(t, c.breaking())

	onlyCheck := moduleChange{Dir: "aws/y", Status: statusChanged, ValidationsChanged: []string{"env"}}
	assert.False(t, onlyCheck.breaking())
	out := render([]moduleChange{onlyCheck}, nil)
	assert.Contains(t, out, "**Check:** `aws/y`")
}

func TestShortAndSpanKeepTheTableRow(t *testing.T) {
	long := `"^(dev|staging|prod)-[a-z0-9-]+-some-long-regex-suffix-here-ééé"`
	got := short(long)
	assert.NotContains(t, strings.ReplaceAll(got, `\|`, ""), "|", "every | must be escaped, also in a cut default")
	assert.True(t, utf8ValidString(got))
	assert.Equal(t, "`` a`b ``", span("a`b"))
}

func utf8ValidString(s string) bool { return utf8.ValidString(s) }
