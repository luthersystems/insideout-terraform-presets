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

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLive_GatherAccountInfo runs the account-info compose against the
// live caller account. Asserts the structural shape — the actual values
// depend on the account.
func TestLive_GatherAccountInfo(t *testing.T) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	if cfg.Region == "" {
		cfg.Region = "eu-west-2"
	}

	stsClient := sts.NewFromConfig(cfg)
	iamClient := iam.NewFromConfig(cfg)

	got := gatherAccountInfo(context.Background(), cfg.Region, stsClient, iamClient)

	dumpJSON(t, "gatherAccountInfo", got)

	// Region is always set (no SDK call, just the cfg field).
	assert.Equal(t, cfg.Region, got["Region"])

	// AccountId / Arn / UserId should be present unless STS is broken.
	if accountID, ok := got["AccountId"].(string); ok {
		assert.Regexp(t, `^\d{12}$`, accountID, "AccountId must be a 12-digit number")
	} else {
		t.Log("warning: GetCallerIdentity did not populate AccountId — STS issue?")
	}
	if arn, ok := got["Arn"].(string); ok {
		assert.Regexp(t, `^arn:aws:.*:\d{12}:`, arn, "Arn must be a real ARN with the account id")
	}
}

// TestLive_DescribeProjectLogGroups_NoFilter exercises the no-prefix
// path. Should return at least one log group on any account that's
// done anything.
func TestLive_DescribeProjectLogGroups_NoFilter(t *testing.T) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	if cfg.Region == "" {
		cfg.Region = "eu-west-2"
	}

	client := cloudwatchlogs.NewFromConfig(cfg)
	got, err := describeProjectLogGroups(context.Background(), client, "")
	require.NoError(t, err)
	t.Logf("describeProjectLogGroups(empty filter) returned %d log groups", len(got))
	if len(got) > 0 {
		t.Logf("first log group: %s", *got[0].LogGroupName)
	}
}

// TestLive_DescribeProjectLogGroups_PrefixScoping requires the env var
// LIVE_PROJECT to be set to a known project name on the account.
// Without it we skip — the prefix-scoping behaviour can't be verified
// without a known matching prefix.
func TestLive_DescribeProjectLogGroups_PrefixScoping(t *testing.T) {
	project := os.Getenv("LIVE_PROJECT")
	if project == "" {
		t.Skip("LIVE_PROJECT not set; export the project name to test prefix scoping")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	if cfg.Region == "" {
		cfg.Region = "eu-west-2"
	}

	client := cloudwatchlogs.NewFromConfig(cfg)
	got, err := describeProjectLogGroups(context.Background(), client, project)
	require.NoError(t, err)
	t.Logf("describeProjectLogGroups(%q) returned %d log groups", project, len(got))
	for _, g := range got {
		assert.Contains(t, *g.LogGroupName, "/aws/"+project,
			"every returned log group must carry the /aws/%s prefix", project)
		t.Logf("  - %s", *g.LogGroupName)
	}
}

// TestLive_EnrichEC2WithConnectURLs runs DescribeInstances against the
// live account, runs enrichEC2WithConnectURLs over the result, and
// dumps the (instanceId → InstanceConnectURL) map. Confirms the
// enrichment composes correctly with the real SDK shape.
func TestLive_EnrichEC2WithConnectURLs(t *testing.T) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	if cfg.Region == "" {
		cfg.Region = "eu-west-2"
	}

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
