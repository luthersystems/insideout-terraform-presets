//go:build integration

// Live AWS smoke tests. Run with:
//
//	go test -tags=integration ./pkg/observability/discovery/aws/... -v -run TestLive
//
// Requires AWS credentials in the environment (e.g. via aws_jump cust2).
// CI does NOT exercise these — the build tag keeps them out of the
// default suite. Used for the #236 / #233 PR's live-validation phase
// to confirm the helper functions actually return sane data against a
// real account.

package aws

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadOrSkip loads ambient AWS config or skips the test. Defaults the
// region to eu-west-2 when the SDK can't infer one (e.g. running with
// just static creds in env vars).
//
// IMPORTANT: config.LoadDefaultConfig succeeds even when no credentials
// are resolvable — failures only surface on the first SDK call. We
// probe with sts.GetCallerIdentity here so a credential-less run skips
// cleanly instead of failing partway through. Per qa-professor review.
func loadOrSkip(t *testing.T) aws.Config {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	if cfg.Region == "" {
		cfg.Region = "eu-west-2"
	}

	// Probe credentials so missing/expired auth produces a Skip,
	// not a confusing late failure inside the actual test body.
	if _, err := sts.NewFromConfig(cfg).GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{}); err != nil {
		t.Skipf("no usable AWS credentials (sts.GetCallerIdentity failed): %v", err)
	}
	return cfg
}

// TestLive_GatherAccountInfo runs the account-info compose against the
// live caller account. After loadOrSkip's auth probe, STS is known to
// work — so AccountId/Arn/UserId are required, not "warn if missing".
func TestLive_GatherAccountInfo(t *testing.T) {
	t.Parallel()
	cfg := loadOrSkip(t)

	stsClient := sts.NewFromConfig(cfg)
	iamClient := iam.NewFromConfig(cfg)

	got := gatherAccountInfo(context.Background(), cfg.Region, stsClient, iamClient)

	dumpJSON(t, "gatherAccountInfo", got)

	assert.Equal(t, cfg.Region, got["Region"])

	accountID, ok := got["AccountId"].(string)
	require.True(t, ok, "AccountId must be populated — loadOrSkip probed STS so this is a real regression if missing")
	assert.Regexp(t, `^\d{12}$`, accountID, "AccountId must be a 12-digit number")

	arn, ok := got["Arn"].(string)
	require.True(t, ok, "Arn must be populated")
	assert.Regexp(t, `^arn:aws:.*:\d{12}:`, arn, "Arn must be a real ARN with the account id")

	userID, ok := got["UserId"].(string)
	require.True(t, ok, "UserId must be populated")
	assert.NotEmpty(t, userID)
}

// TestLive_DescribeProjectLogGroups_NoFilter exercises the no-prefix
// path. Should return at least one log group on any account that's
// done anything.
func TestLive_DescribeProjectLogGroups_NoFilter(t *testing.T) {
	t.Parallel()
	cfg := loadOrSkip(t)

	client := cloudwatchlogs.NewFromConfig(cfg)
	got, err := describeProjectLogGroups(context.Background(), client, "")
	require.NoError(t, err)
	t.Logf("describeProjectLogGroups(empty filter) returned %d log groups", len(got))
	if len(got) > 0 {
		t.Logf("first log group: %s", *got[0].LogGroupName)
	}
}

// TestLive_DescribeProjectLogGroups_SubstringScoping requires the env
// var LIVE_PROJECT to be set to a known project name on the account.
// Without it we skip — the substring scoping can't be verified without
// a known matching project. The expectation is non-zero results AND
// every returned log group containing the project name as a substring
// (not necessarily the `/aws/<project>` prefix — see the helper's
// header comment for why prefix-scoping was wrong).
func TestLive_DescribeProjectLogGroups_SubstringScoping(t *testing.T) {
	t.Parallel()
	project := os.Getenv("LIVE_PROJECT")
	if project == "" {
		t.Skip("LIVE_PROJECT not set; export the project name to test substring scoping")
	}

	cfg := loadOrSkip(t)

	client := cloudwatchlogs.NewFromConfig(cfg)
	got, err := describeProjectLogGroups(context.Background(), client, project)
	require.NoError(t, err)
	t.Logf("describeProjectLogGroups(%q) returned %d log groups", project, len(got))
	require.NotEmpty(t, got,
		"a real project on the account should return at least one log group via substring scoping; "+
			"if this returned zero, either LIVE_PROJECT is wrong or the project genuinely emits no logs")
	for _, g := range got {
		assert.Contains(t, *g.LogGroupName, project,
			"every returned log group must contain %q as a substring (LogGroupNamePattern contract)", project)
		t.Logf("  - %s", *g.LogGroupName)
	}
}

// TestLive_EnrichEC2WithConnectURLs runs DescribeInstances against the
// live account, runs enrichEC2WithConnectURLs over the result, and
// dumps the (instanceId → InstanceConnectURL) map. Confirms the
// enrichment composes correctly with the real SDK shape.
func TestLive_EnrichEC2WithConnectURLs(t *testing.T) {
	t.Parallel()
	cfg := loadOrSkip(t)

	ec2Client := ec2.NewFromConfig(cfg)
	out, err := ec2Client.DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{})
	require.NoError(t, err)

	enriched := enrichEC2WithConnectURLs(cfg.Region, out.Reservations)
	t.Logf("enriched %d reservations", len(out.Reservations))

	enrichedSlice, ok := enriched.([]map[string]any)
	require.True(t, ok, "enrichment must produce []map[string]any not the raw fallback")

	for _, res := range enrichedSlice {
		instances, _ := res["Instances"].([]any)
		for _, inst := range instances {
			m, _ := inst.(map[string]any)
			id, _ := m["InstanceId"].(string)
			state, _ := m["State"].(map[string]any)
			stateName, _ := state["Name"].(string)
			url, _ := m["InstanceConnectURL"].(string)
			t.Logf("  %s state=%-12s connect_url_present=%t", id, stateName, url != "")
			if stateName != "running" {
				assert.Empty(t, url, "non-running %s must NOT have a URL", id)
			}
		}
	}
}

func dumpJSON(t *testing.T, label string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Logf("%s: marshal failed: %v", label, err)
		return
	}
	t.Logf("%s:\n%s", label, b)
}
