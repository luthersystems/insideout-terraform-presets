// EC2 Instance Connect URL enrichment tests. Pure-function tests, no
// AWS SDK calls — covers the running/stopped state filter, mixed
// reservation merging, and the JSON round-trip fallback contract.
//
// Ported from reliable internal/agentapi/aws_inspect_test.go::
// TestEnrichEC2WithConnectURLs.

package aws

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runningInstance constructs a minimal Reservation with one running
// instance — the most common case the URL enrichment must hit.
func runningInstance(id string) ec2types.Reservation {
	return ec2types.Reservation{
		Instances: []ec2types.Instance{
			{
				InstanceId: aws.String(id),
				State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
			},
		},
	}
}

func stoppedInstance(id string) ec2types.Reservation {
	return ec2types.Reservation{
		Instances: []ec2types.Instance{
			{
				InstanceId: aws.String(id),
				State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameStopped},
			},
		},
	}
}

// urlsFromEnriched walks the JSON-friendly enriched output and collects
// (instanceId → InstanceConnectURL) pairs. Returns nil when the value
// shape isn't enriched (e.g. the JSON-fallback path returned the
// original []ec2types.Reservation).
func urlsFromEnriched(t *testing.T, got any) map[string]string {
	t.Helper()
	enriched, ok := got.([]map[string]any)
	if !ok {
		return nil
	}
	urls := make(map[string]string, len(enriched))
	for _, res := range enriched {
		instances, ok := res["Instances"].([]any)
		require.True(t, ok, "enriched reservation must carry []Instances")
		for _, inst := range instances {
			m, ok := inst.(map[string]any)
			require.True(t, ok)
			id, _ := m["InstanceId"].(string)
			if u, ok := m["InstanceConnectURL"].(string); ok {
				urls[id] = u
			} else {
				urls[id] = ""
			}
		}
	}
	return urls
}

func TestEnrichEC2WithConnectURLs_RunningGetsURL(t *testing.T) {
	t.Parallel()
	got := enrichEC2WithConnectURLs("eu-west-2", []ec2types.Reservation{runningInstance("i-running")})
	urls := urlsFromEnriched(t, got)
	require.NotNil(t, urls, "running instance must produce enriched output, not the raw fallback")

	url, ok := urls["i-running"]
	require.True(t, ok)
	assert.NotEmpty(t, url, "running instance must carry InstanceConnectURL")
	assert.True(t, strings.HasPrefix(url, "https://eu-west-2.console.aws.amazon.com/ec2-instance-connect/ssh"),
		"URL must point at the EC2 Instance Connect console for the right region: %s", url)
	assert.Contains(t, url, "instanceId=i-running")
	assert.Contains(t, url, "region=eu-west-2")
}

func TestEnrichEC2WithConnectURLs_StoppedNoURL(t *testing.T) {
	t.Parallel()
	got := enrichEC2WithConnectURLs("us-east-1", []ec2types.Reservation{stoppedInstance("i-stopped")})
	urls := urlsFromEnriched(t, got)
	require.NotNil(t, urls)

	url, ok := urls["i-stopped"]
	require.True(t, ok)
	assert.Empty(t, url, "stopped instance must NOT carry an InstanceConnectURL — the link would 500 in the console")
}

func TestEnrichEC2WithConnectURLs_EmptyReservationsReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := enrichEC2WithConnectURLs("eu-west-2", []ec2types.Reservation{})
	enriched, ok := got.([]map[string]any)
	require.True(t, ok, "empty reservations must still produce a JSON-friendly slice (not raw []ec2types.Reservation)")
	assert.Empty(t, enriched)
}

// TestEnrichEC2WithConnectURLs_MixedRunningAndStopped — the running
// instance gets a URL, the stopped one doesn't, both stay in the
// output.
func TestEnrichEC2WithConnectURLs_MixedRunningAndStopped(t *testing.T) {
	t.Parallel()
	mixed := []ec2types.Reservation{
		runningInstance("i-up"),
		stoppedInstance("i-down"),
	}

	got := enrichEC2WithConnectURLs("ap-southeast-1", mixed)
	urls := urlsFromEnriched(t, got)
	require.NotNil(t, urls)
	require.Len(t, urls, 2, "both reservations must survive the enrichment")

	assert.Contains(t, urls["i-up"], "instanceId=i-up", "running instance must be enriched")
	assert.Empty(t, urls["i-down"], "stopped instance must not be enriched")
}

// TestEnrichEC2WithConnectURLs_MultipleInstancesPerReservation — one
// reservation can carry multiple instances; the per-instance state
// check must apply individually.
func TestEnrichEC2WithConnectURLs_MultipleInstancesPerReservation(t *testing.T) {
	t.Parallel()
	res := ec2types.Reservation{
		Instances: []ec2types.Instance{
			{InstanceId: aws.String("i-a"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
			{InstanceId: aws.String("i-b"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameStopped}},
			{InstanceId: aws.String("i-c"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
		},
	}

	got := enrichEC2WithConnectURLs("eu-west-2", []ec2types.Reservation{res})
	urls := urlsFromEnriched(t, got)
	require.NotNil(t, urls)
	require.Len(t, urls, 3)

	assert.Contains(t, urls["i-a"], "instanceId=i-a")
	assert.Empty(t, urls["i-b"])
	assert.Contains(t, urls["i-c"], "instanceId=i-c")
}

// TestEnrichEC2WithConnectURLs_RegionPropagatesIntoURL — the URL
// hardcodes the region in two places (host and query string); both
// must reflect the caller's region.
func TestEnrichEC2WithConnectURLs_RegionPropagatesIntoURL(t *testing.T) {
	t.Parallel()
	got := enrichEC2WithConnectURLs("us-east-2", []ec2types.Reservation{runningInstance("i-1")})
	urls := urlsFromEnriched(t, got)
	require.NotNil(t, urls)
	url := urls["i-1"]

	assert.True(t, strings.HasPrefix(url, "https://us-east-2.console.aws.amazon.com/"), "host must use the caller region: %s", url)
	assert.Contains(t, url, "region=us-east-2", "query must use the caller region: %s", url)
}

// TestEnrichEC2WithConnectURLs_EmptyInstanceIDSkipped — instances with
// no InstanceId must not gain a URL nor crash the enrichment.
func TestEnrichEC2WithConnectURLs_EmptyInstanceIDSkipped(t *testing.T) {
	t.Parallel()
	res := ec2types.Reservation{
		Instances: []ec2types.Instance{
			{InstanceId: aws.String(""), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
		},
	}
	got := enrichEC2WithConnectURLs("eu-west-2", []ec2types.Reservation{res})
	urls := urlsFromEnriched(t, got)
	require.NotNil(t, urls)
	url := urls[""]
	assert.Empty(t, url, "instances without an InstanceId must not produce a URL")
}

// TestEnrichEC2WithConnectURLs_PreservesNonStateInstanceFields — the
// JSON round-trip must preserve other fields the panel renders (e.g.
// PrivateIpAddress, Tags) so the enrichment is purely additive.
func TestEnrichEC2WithConnectURLs_PreservesNonStateInstanceFields(t *testing.T) {
	t.Parallel()
	res := ec2types.Reservation{
		Instances: []ec2types.Instance{
			{
				InstanceId:       aws.String("i-1"),
				State:            &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
				PrivateIpAddress: aws.String("10.0.0.42"),
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String("bastion")},
				},
			},
		},
	}
	got := enrichEC2WithConnectURLs("eu-west-2", []ec2types.Reservation{res})
	enriched, ok := got.([]map[string]any)
	require.True(t, ok)
	require.Len(t, enriched, 1)

	instances, ok := enriched[0]["Instances"].([]any)
	require.True(t, ok)
	require.Len(t, instances, 1)

	inst := instances[0].(map[string]any)
	assert.Equal(t, "10.0.0.42", inst["PrivateIpAddress"], "PrivateIpAddress must survive the JSON round-trip")
	tags, ok := inst["Tags"].([]any)
	require.True(t, ok)
	require.Len(t, tags, 1)
	tag := tags[0].(map[string]any)
	assert.Equal(t, "Name", tag["Key"])
	assert.Equal(t, "bastion", tag["Value"])
	assert.NotEmpty(t, inst["InstanceConnectURL"])
}
