package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awsVPCNATCfg is a thin shim over cfgWithAWSVPC (mapper_test.go:182) that
// only sets EnableNATGateway — the only field this validator inspects.
// Reuse keeps the test refactor-resilient: any future field added to
// Config.AWSVPC only needs to touch the shared helper.
func awsVPCNATCfg(enable *bool) *Config {
	return cfgWithAWSVPC(nil, enable, nil)
}

// TestValidateAWSVPCNATConsistency pins every branch of the #389 validator.
// The matrix locks in cloud-gating (GCP / empty are no-ops), the bug shape
// (Public VPC + no private-subnet components + EnableNATGateway=true), and
// every adjacent "legitimate" shape that must NOT emit the issue.
func TestValidateAWSVPCNATConsistency(t *testing.T) {
	t.Parallel()
	boolPtr := func(v bool) *bool { return &v }

	cases := []struct {
		name      string
		cloud     string
		comps     *Components
		cfg       *Config
		wantIssue bool
	}{
		// Bug shape — must fire.
		{
			name:      "Public VPC + no consumers + EnableNATGateway=true (#389 bug shape)",
			cloud:     "aws",
			comps:     &Components{AWSVPC: "Public VPC"},
			cfg:       awsVPCNATCfg(boolPtr(true)),
			wantIssue: true,
		},

		// Cloud gating.
		{name: "gcp cloud is a no-op", cloud: "gcp", comps: &Components{AWSVPC: "Public VPC"}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "empty cloud is a no-op", cloud: "", comps: &Components{AWSVPC: "Public VPC"}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "case-insensitive cloud match", cloud: "AWS", comps: &Components{AWSVPC: "Public VPC"}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: true},

		// Nil-input safety.
		{name: "nil comps is a no-op", cloud: "aws", comps: nil, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "nil cfg is a no-op", cloud: "aws", comps: &Components{AWSVPC: "Public VPC"}, cfg: nil, wantIssue: false},
		{name: "nil cfg.AWSVPC is a no-op", cloud: "aws", comps: &Components{AWSVPC: "Public VPC"}, cfg: &Config{}, wantIssue: false},
		{name: "nil cfg.AWSVPC.EnableNATGateway is a no-op", cloud: "aws", comps: &Components{AWSVPC: "Public VPC"}, cfg: awsVPCNATCfg(nil), wantIssue: false},

		// VPC-shape gating.
		{name: "Private VPC is a no-op (NAT is the documented default)", cloud: "aws", comps: &Components{AWSVPC: "Private VPC"}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "empty AWSVPC is a no-op", cloud: "aws", comps: &Components{}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "EnableNATGateway=false on Public VPC is a no-op (the mapper heals the needs-private inverse)", cloud: "aws", comps: &Components{AWSVPC: "Public VPC"}, cfg: awsVPCNATCfg(boolPtr(false)), wantIssue: false},

		// Consumer-presence gating — when a private-subnet-needing component
		// is present, EnableNATGateway=true is legitimate. Cover every
		// member of stackNeedsPrivateSubnets explicitly so a future
		// regression that drops one from the predicate gets caught here.
		{name: "AWSEKS present -> legitimate NAT, no issue", cloud: "aws", comps: &Components{AWSVPC: "Public VPC", AWSEKS: boolPtr(true)}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "AWSECS present -> legitimate NAT, no issue", cloud: "aws", comps: &Components{AWSVPC: "Public VPC", AWSECS: boolPtr(true)}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "AWSRDS present -> legitimate NAT, no issue", cloud: "aws", comps: &Components{AWSVPC: "Public VPC", AWSRDS: boolPtr(true)}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "AWSElastiCache present -> legitimate NAT, no issue", cloud: "aws", comps: &Components{AWSVPC: "Public VPC", AWSElastiCache: boolPtr(true)}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "AWSOpenSearch present -> legitimate NAT, no issue (the v5-stack predecessor of #389)", cloud: "aws", comps: &Components{AWSVPC: "Public VPC", AWSOpenSearch: boolPtr(true)}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
		{name: "AWSEC2 present -> legitimate NAT, no issue", cloud: "aws", comps: &Components{AWSVPC: "Public VPC", AWSEC2: "Intel"}, cfg: awsVPCNATCfg(boolPtr(true)), wantIssue: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ValidateAWSVPCNATConsistency(tc.cloud, tc.comps, tc.cfg)
			if !tc.wantIssue {
				require.Empty(t, got, "expected no issues, got %#v", got)
				return
			}
			// require gates the indexing into got[0]; the per-field checks
			// below are the assertions under test and use assert so a
			// single mismatch surfaces every other field-level discrepancy
			// in one run (per golang-guidance §13).
			require.Len(t, got, 1, "expected exactly one issue")
			assert.Equal(t, "aws_vpc_stale_nat_gateway", got[0].Code)
			assert.Equal(t, "cfg.aws_vpc.enable_nat_gateway", got[0].Field)
			assert.Equal(t, "true", got[0].Value)
			assert.NotEmpty(t, got[0].Reason)
			assert.NotEmpty(t, got[0].Suggestion)
			assert.Contains(t, got[0].Reason, "#389", "reason should reference the issue so downstream callers can find context")
		})
	}
}

// TestComposeStackWithIssues_AWSVPCStaleNATGateway_Issue389 closes the
// integration loop end-to-end. It composes the exact stack from the bug
// report (Public VPC, no private-subnet consumers, stale
// cfg.AWSVPC.EnableNATGateway=true) and asserts:
//
//   - the emitted vpc.auto.tfvars contains enable_nat_gateway=false (Layer
//     1a coercion makes the deploy correct)
//   - Result.Issues contains aws_vpc_stale_nat_gateway (Layer 1b surfacing)
//   - StrictValidate=true escalates that issue to an error so callers that
//     prefer hard-fail get it
func TestComposeStackWithIssues_AWSVPCStaleNATGateway_Issue389(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	// Build the components blob from the bug report.
	comps := &Components{
		Cloud:             "AWS",
		AWSS3:             boolPtr(true),
		AWSKMS:            boolPtr(true),
		AWSVPC:            "Public VPC",
		AWSLambda:         boolPtr(true),
		Architecture:      "Serverless",
		AWSSecretsManager: boolPtr(true),
		AWSCloudWatchLogs: boolPtr(true),
	}
	cfg := awsVPCNATCfg(boolPtr(true))
	cfg.Region = "us-east-1"

	opts := ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSS3, KeyAWSKMS, KeyAWSLambda, KeyAWSSecretsManager, KeyAWSCloudWatchLogs},
		Comps:        comps,
		Cfg:          cfg,
		Project:      "test-389",
		Region:       "us-east-1",
	}

	c := newTestClient()

	t.Run("default mode: coerces tfvars and surfaces the issue", func(t *testing.T) {
		out, err := c.ComposeStackWithIssues(opts)
		require.NoError(t, err, "default mode should not hard-fail; it surfaces the issue")
		require.NotNil(t, out)

		// Confirm the emitted tfvars no longer contains the bad pair.
		// The vpc-namespaced key for enable_nat_gateway is vpc_enable_nat_gateway.
		tfvarsBytes, ok := out.Files["/aws_vpc.auto.tfvars"]
		require.True(t, ok, "expected /aws_vpc.auto.tfvars in composed files; got: %v", filesKeys(out.Files))
		// EmitAutoTFVars pads names for alignment, so collapse whitespace
		// before asserting key=value pairs.
		tfvars := strings.Join(strings.Fields(string(tfvarsBytes)), " ")
		require.Contains(t, tfvars, "aws_vpc_enable_nat_gateway = false", "Layer 1a coercion must zero NAT in the emitted tfvars")
		require.Contains(t, tfvars, "aws_vpc_enable_private_subnets = false", "Public VPC + no consumers still disables private subnets")

		// Confirm the warning surfaced.
		var found *ValidationIssue
		for i := range out.Issues {
			if out.Issues[i].Code == "aws_vpc_stale_nat_gateway" {
				found = &out.Issues[i]
				break
			}
		}
		require.NotNil(t, found, "expected aws_vpc_stale_nat_gateway in Issues; got: %#v", out.Issues)
		assert.Equal(t, "cfg.aws_vpc.enable_nat_gateway", found.Field)
		assert.Equal(t, "true", found.Value)
		assert.Contains(t, found.Reason, "Public VPC")
	})

	t.Run("StrictValidate=true escalates the issue to an error", func(t *testing.T) {
		strictOpts := opts
		strictOpts.StrictValidate = true
		_, err := c.ComposeStackWithIssues(strictOpts)
		require.Error(t, err, "StrictValidate must promote the warning to a hard error")
		// summarizeIssues renders "Field: Reason"; assert on the field path
		// (stable) and that the bug-source marker "#389" is included
		// (the Code itself isn't surfaced in summarizeIssues output).
		assert.Contains(t, err.Error(), "cfg.aws_vpc.enable_nat_gateway",
			"error should name the failing field so callers can route on it; got: %v", err)
		assert.Contains(t, err.Error(), "#389",
			"error should carry the issue reference; got: %v", err)
	})
}

// findIssue returns the first issue with the given code, or nil. Used to
// keep the cloud-resolution subtests below readable without growing a full
// suite-level helper.
func findIssue(issues []ValidationIssue, code string) *ValidationIssue {
	for i := range issues {
		if issues[i].Code == code {
			return &issues[i]
		}
	}
	return nil
}

// TestValidateAll_AWSVPCStaleNATGateway_CloudResolution locks in BOTH halves
// of the cloud-resolution contract that ValidateAll uses to gate the AWS-VPC
// cross-field check:
//
//  1. No-opts fallback (the original gap): dry-run callers (per
//     ValidateAll's docstring) pass no ComposeStackOpts; the validator
//     must still run when Comps.Cloud carries the cloud identity.
//  2. Explicit-opts precedence: when both opts.Cloud and Comps.Cloud are
//     present, opts.Cloud wins. Mirrors ValidateOpts's behaviour
//     (validate_gcp_project.go:30-32); a regression that read Comps.Cloud
//     unconditionally would slip past test 1 alone.
func TestValidateAll_AWSVPCStaleNATGateway_CloudResolution(t *testing.T) {
	t.Parallel()
	boolPtr := func(v bool) *bool { return &v }

	t.Run("no opts: fallback to Comps.Cloud (AWS-shaped bug fires)", func(t *testing.T) {
		t.Parallel()
		comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC"}
		cfg := awsVPCNATCfg(boolPtr(true))

		issues := ValidateAll(comps, cfg, nil, nil, nil, nil)

		got := findIssue(issues, "aws_vpc_stale_nat_gateway")
		require.NotNil(t, got, "ValidateAll(no opts) must emit aws_vpc_stale_nat_gateway when Comps.Cloud=AWS; got: %#v", issues)
		assert.Equal(t, "cfg.aws_vpc.enable_nat_gateway", got.Field)
	})

	t.Run("explicit opts.Cloud=gcp wins over Comps.Cloud=AWS (no AWS issue emitted)", func(t *testing.T) {
		t.Parallel()
		// Mismatch case: opts.Cloud must override Comps.Cloud. The
		// composer's chat-preview path can deliberately pass an opts.Cloud
		// that differs from leftover Comps.Cloud (see ValidateOpts test
		// TestValidateOpts/"opts.Cloud=aws is a no-op even when
		// Comps.Cloud=GCP" at validate_gcp_project_test.go:81-92). A
		// regression that read Comps.Cloud unconditionally for the AWS
		// check would emit a spurious aws_vpc_stale_nat_gateway here.
		comps := &Components{Cloud: "AWS", AWSVPC: "Public VPC"}
		cfg := awsVPCNATCfg(boolPtr(true))

		issues := ValidateAll(comps, cfg, nil, nil, nil, nil, ComposeStackOpts{Cloud: "gcp"})

		got := findIssue(issues, "aws_vpc_stale_nat_gateway")
		assert.Nil(t, got, "explicit opts.Cloud=gcp must suppress the AWS-side check even when Comps.Cloud=AWS; got: %#v", issues)
	})
}

// filesKeys returns the file paths from a Files map, for legible failure messages.
func filesKeys(f Files) []string {
	out := make([]string, 0, len(f))
	for k := range f {
		out = append(out, k)
	}
	return out
}
