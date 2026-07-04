//go:build cust3live

// Package cust3live_test holds env-gated LIVE terraform tests that exercise the
// composer's generated stacks against a REAL terraform binary and REAL AWS in
// the cust3 TEST account (031780745048). It is plan/validate-ONLY by design:
// the checked-in test never runs `terraform apply` or `terraform destroy`. The
// apply/destroy loop stays manual via cmd/composetest.
//
// The tests are compiled only under the `cust3live` build tag, so normal CI
// (`go test ./...`) never builds this package. They further self-skip unless an
// AWS access key is present in the environment and `terraform` is on PATH.
//
// # How to run
//
// Credentials resolve from 1Password (item: claude-web-tf-cust3, vault
// Reliable-Dev). The IAM user has exactly one permission — sts:AssumeRole into
// arn:aws:iam::031780745048:role/claude-web-tf-session (admin in the cust3 TEST
// account, deny-guardrailed). Point op run at an env file whose
// AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are op:// references:
//
//	op run --env-file=<env-file-with-op-refs> -- \
//	  go test -tags cust3live ./tests/cust3live/ -v
//
// See tests/cust3live/README.md for the full runbook and rationale.
package cust3live_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

const (
	// cust3Account is the cust3 TEST AWS account the deny-guardrailed role
	// lives in. Documented here so a failing run points at the right account.
	cust3Account = "031780745048"

	// defaultSessionRoleARN is the role the composed provider blocks assume via
	// STS. Override with CUST3_SESSION_ROLE_ARN.
	defaultSessionRoleARN = "arn:aws:iam::031780745048:role/claude-web-tf-session"

	// skipMsg tells an operator how to actually run these live tests.
	skipMsg = "cust3live: set AWS creds + terraform on PATH to run. " +
		"Creds resolve from 1Password (item claude-web-tf-cust3, vault Reliable-Dev); run:\n" +
		"  op run --env-file=<env-file-with-op-refs> -- go test -tags cust3live ./tests/cust3live/ -v"
)

// planPattern matches a clean terraform plan with real diffs allowed to add but
// nothing to change or destroy — the signal that a real STS assume + AWS API
// round trip succeeded and the composed stack is coherent.
var planPattern = regexp.MustCompile(`Plan: \d+ to add, 0 to change, 0 to destroy`)

// gateLive skips the test unless a live run is possible, and — critically —
// scrubs stale session/profile env that silently breaks a raw access key.
func gateLive(t *testing.T) {
	t.Helper()
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip(skipMsg)
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip(skipMsg)
	}
	// FIRST thing after the gate: a leftover AWS_SESSION_TOKEN /
	// AWS_SECURITY_TOKEN / AWS_PROFILE from a prior assume-role session makes
	// the raw IAM user key fail with InvalidClientTokenId ("The security token
	// included in the request is invalid") — a real gotcha we hit. The cust3
	// key is a bare long-lived access key; it must be used WITHOUT a session
	// token, and no profile must shadow it.
	os.Unsetenv("AWS_SESSION_TOKEN")
	os.Unsetenv("AWS_SECURITY_TOKEN")
	os.Unsetenv("AWS_PROFILE")
}

// sessionRoleARN returns the role to assume, honoring CUST3_SESSION_ROLE_ARN.
func sessionRoleARN() string {
	if v := strings.TrimSpace(os.Getenv("CUST3_SESSION_ROLE_ARN")); v != "" {
		return v
	}
	return defaultSessionRoleARN
}

// writeStack materializes a composed Files map into dir, stripping the leading
// "/" from each composer path (they are rooted at "/") and creating parents.
func writeStack(t *testing.T, dir string, files composer.Files) {
	t.Helper()
	for path, content := range files {
		dst := filepath.Join(dir, strings.TrimPrefix(path, "/"))
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
		require.NoError(t, os.WriteFile(dst, content, 0o644))
	}
}

// runTF runs a terraform subcommand in dir and returns combined output. It
// NEVER prints or captures environment values — only argv and stdout/stderr,
// which terraform itself does not echo secrets into.
func runTF(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("terraform", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tail returns the last n lines of s — used to keep failure messages readable
// without dumping an entire terraform log (and without ever touching env).
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TestCust3Live_ComposedStackPlans composes a real aws_s3 stack, writes it to a
// temp dir, and runs `terraform init` + `terraform plan` against the live cust3
// account (a real STS assume + AWS API round trip). PLAN ONLY — never applies.
func TestCust3Live_ComposedStackPlans(t *testing.T) {
	gateLive(t)

	c := composer.New()
	res, err := c.ComposeStackWithIssues(composer.ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []composer.ComponentKey{"aws_s3"},
		Comps:        &composer.Components{Cloud: "aws"},
		Cfg:          &composer.Config{Region: "us-west-2"},
		Project:      "clw3ci",
		Region:       "us-west-2",
	})
	require.NoError(t, err, "composing aws_s3 stack must not error")
	for _, is := range res.Issues {
		t.Logf("compose issue: code=%s field=%s value=%s reason=%s", is.Code, is.Field, is.Value, is.Reason)
	}
	require.NotEmpty(t, res.Files, "composer must emit files")

	dir := t.TempDir()
	writeStack(t, dir, res.Files)

	if out, err := runTF(t, dir, "init", "-input=false", "-no-color"); err != nil {
		t.Fatalf("terraform init failed: %v\n--- tail ---\n%s", err, tail(out, 40))
	}

	planOut, err := runTF(t, dir, "plan", "-input=false", "-no-color",
		"-var", "bootstrap_role="+sessionRoleARN())
	if err != nil {
		t.Fatalf("terraform plan failed (real STS assume into %s / account %s): %v\n--- tail ---\n%s",
			sessionRoleARN(), cust3Account, err, tail(planOut, 40))
	}
	require.Regexpf(t, planPattern, planOut,
		"plan must show 'Plan: N to add, 0 to change, 0 to destroy':\n--- tail ---\n%s", tail(planOut, 40))
}

// TestCust3Live_ImportOnlyStackValidates composes an import-only stack (no
// SelectedKeys) carrying a synthetic Imported fixture — one aws_route_table
// with inline route attrs and one aws_s3_bucket — and runs `terraform init` +
// `terraform validate` against the REAL pinned AWS provider. Fake import IDs are
// fine because this test never plans (import blocks with fake IDs would fail a
// plan). This catches the odb_network_arn class: a schema-level rejection of
// emitted literals (e.g. an inline route element missing the provider-Required
// odb_network_arn) surfaces at `terraform validate` against the real provider.
func TestCust3Live_ImportOnlyStackValidates(t *testing.T) {
	gateLive(t)

	// aws_route_table with an inline route — exercises the computed-nested
	// route literal path (dropped by the emitter; a regression that re-emitted
	// it without the Required odb_network_arn would fail validate here).
	rtAttrs, err := json.Marshal(&generated.AWSRouteTable{
		VPCID: generated.LiteralOf("vpc-0abc1234def567890"),
		Tags:  map[string]*generated.Value[string]{"Name": generated.LiteralOf("clw3ci-rtb")},
		Route: []generated.AWSRouteTableRoute{{
			CIDRBlock: generated.LiteralOf("0.0.0.0/0"),
			GatewayID: generated.LiteralOf("igw-0abc1234def567890"),
		}},
	})
	require.NoError(t, err)

	s3Attrs, err := json.Marshal(&generated.AWSS3Bucket{
		Bucket: generated.LiteralOf("clw3ci-imported-bucket"),
		Region: generated.LiteralOf("us-west-2"),
	})
	require.NoError(t, err)

	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_route_table",
				Address:  "aws_route_table.main",
				Region:   "us-west-2",
				ImportID: "rtb-0fake1234567890ab", // fake — validate-only, never planned
			},
			Tier:  imported.TierImportedFlat,
			Attrs: rtAttrs,
		},
		{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_s3_bucket",
				Address:  "aws_s3_bucket.imported",
				Region:   "us-west-2",
				ImportID: "clw3ci-imported-bucket", // fake — validate-only
			},
			Tier:  imported.TierImportedFlat,
			Attrs: s3Attrs,
		},
	}

	c := composer.New()
	res, err := c.ComposeStackWithIssues(composer.ComposeStackOpts{
		Cloud:           "aws",
		SelectedKeys:    nil, // import-only: no preset modules
		Comps:           &composer.Components{Cloud: "aws"},
		Cfg:             &composer.Config{Region: "us-west-2"},
		Project:         "clw3ci",
		Region:          "us-west-2",
		Imported:        irs,
		ImportProjectID: "clw3ci",
		ImportSessionID: "sess_clw3ci",
	})
	require.NoError(t, err, "composing import-only stack must not error")
	for _, is := range res.Issues {
		t.Logf("compose issue: code=%s field=%s value=%s reason=%s", is.Code, is.Field, is.Value, is.Reason)
	}
	require.NotEmpty(t, res.Files, "composer must emit files for the import-only stack")

	dir := t.TempDir()
	writeStack(t, dir, res.Files)

	if out, err := runTF(t, dir, "init", "-input=false", "-no-color"); err != nil {
		t.Fatalf("terraform init failed: %v\n--- tail ---\n%s", err, tail(out, 40))
	}

	if out, err := runTF(t, dir, "validate", "-no-color"); err != nil {
		t.Fatalf("terraform validate failed against the real pinned provider "+
			"(odb_network_arn-class schema rejection?): %v\n--- tail ---\n%s", err, tail(out, 40))
	}
}
