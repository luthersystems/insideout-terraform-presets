// AWS Bedrock AgentCore Gateway inspector tests (issue #763).
//
// Pins the #255 contract: an empty list-gateways response MUST marshal as
// JSON `[]`, never `null`. Also pins the ListGateways→GetGateway ARN-
// resolution path (GatewaySummary carries no ARN; the AWS/Bedrock-AgentCore
// Resource dimension value comes from GetGateway), NextToken pagination, and
// the metrics-routing arm.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	agentcoretypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAgentCoreClient struct {
	// listPages is returned one page per ListGateways call (to exercise
	// NextToken pagination). A single-element slice means a single page.
	listPages []*bedrockagentcorecontrol.ListGatewaysOutput
	listCalls int

	// getArns maps a gateway id to the ARN GetGateway returns for it.
	getArns  map[string]string
	getCalls []string

	listErr error
	getErr  error
}

func (f *fakeAgentCoreClient) ListGateways(_ context.Context, in *bedrockagentcorecontrol.ListGatewaysInput, _ ...func(*bedrockagentcorecontrol.Options)) (*bedrockagentcorecontrol.ListGatewaysOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	idx := f.listCalls
	f.listCalls++
	if idx >= len(f.listPages) {
		return &bedrockagentcorecontrol.ListGatewaysOutput{}, nil
	}
	return f.listPages[idx], nil
}

func (f *fakeAgentCoreClient) GetGateway(_ context.Context, in *bedrockagentcorecontrol.GetGatewayInput, _ ...func(*bedrockagentcorecontrol.Options)) (*bedrockagentcorecontrol.GetGatewayOutput, error) {
	f.getCalls = append(f.getCalls, aws.ToString(in.GatewayIdentifier))
	if f.getErr != nil {
		return nil, f.getErr
	}
	arn := f.getArns[aws.ToString(in.GatewayIdentifier)]
	return &bedrockagentcorecontrol.GetGatewayOutput{
		GatewayId:  in.GatewayIdentifier,
		GatewayArn: aws.String(arn),
	}, nil
}

// TestListAgentCoreGateways_EmptyResult — #255 contract: empty response is
// JSON `[]`, not `null`.
func TestListAgentCoreGateways_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listAgentCoreGateways(context.Background(), &fakeAgentCoreClient{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty gateway list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListAgentCoreGateways_ExplicitEmptyItemsNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeAgentCoreClient{
		listPages: []*bedrockagentcorecontrol.ListGatewaysOutput{
			{Items: []agentcoretypes.GatewaySummary{}}, // explicitly empty
		},
	}
	got, err := listAgentCoreGateways(context.Background(), client)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

// TestListAgentCoreGateways_ResolvesARNViaGetGateway — the headline behavior:
// GatewaySummary carries only the id, so the inspector must call GetGateway to
// resolve the ARN (the Resource dimension value the metrics namespace keys on).
func TestListAgentCoreGateways_ResolvesARNViaGetGateway(t *testing.T) {
	t.Parallel()
	client := &fakeAgentCoreClient{
		listPages: []*bedrockagentcorecontrol.ListGatewaysOutput{
			{Items: []agentcoretypes.GatewaySummary{
				{GatewayId: aws.String("gw-abc"), Name: aws.String("support-tools"), Status: agentcoretypes.GatewayStatusReady},
			}},
		},
		getArns: map[string]string{
			"gw-abc": "arn:aws:bedrock-agentcore:us-east-1:111111111111:gateway/gw-abc",
		},
	}
	got, err := listAgentCoreGateways(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "gw-abc", got[0].GatewayID)
	assert.Equal(t, "support-tools", got[0].Name)
	assert.Equal(t, "arn:aws:bedrock-agentcore:us-east-1:111111111111:gateway/gw-abc", got[0].GatewayArn,
		"the ARN must be resolved from GetGateway — it is the Resource dimension value the metrics panel keys on")
	assert.Equal(t, []string{"gw-abc"}, client.getCalls, "GetGateway must be called once per listed gateway id")
}

// TestListAgentCoreGateways_Paginates — NextToken must drive a second
// ListGateways call and both pages must be collected.
func TestListAgentCoreGateways_Paginates(t *testing.T) {
	t.Parallel()
	client := &fakeAgentCoreClient{
		listPages: []*bedrockagentcorecontrol.ListGatewaysOutput{
			{
				Items:     []agentcoretypes.GatewaySummary{{GatewayId: aws.String("gw-1")}},
				NextToken: aws.String("page2"),
			},
			{
				Items: []agentcoretypes.GatewaySummary{{GatewayId: aws.String("gw-2")}},
			},
		},
		getArns: map[string]string{
			"gw-1": "arn:aws:bedrock-agentcore:us-east-1:111111111111:gateway/gw-1",
			"gw-2": "arn:aws:bedrock-agentcore:us-east-1:111111111111:gateway/gw-2",
		},
	}
	got, err := listAgentCoreGateways(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 2, client.listCalls, "NextToken must drive a second ListGateways page")
	assert.Equal(t, "gw-1", got[0].GatewayID)
	assert.Equal(t, "gw-2", got[1].GatewayID)
}

func TestListAgentCoreGateways_ListAPIError(t *testing.T) {
	t.Parallel()
	_, err := listAgentCoreGateways(context.Background(), &fakeAgentCoreClient{listErr: errors.New("AccessDenied")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

// TestListAgentCoreGateways_GetGatewayError — a per-id GetGateway failure must
// NOT abort the whole listing. The gateway is still returned (id-keyed, ARN
// left empty) so one throttled/AccessDenied gateway can't collapse the panel to
// the #255 empty state. Matches the documented continue-on-error behavior.
func TestListAgentCoreGateways_GetGatewayError(t *testing.T) {
	t.Parallel()
	client := &fakeAgentCoreClient{
		listPages: []*bedrockagentcorecontrol.ListGatewaysOutput{
			{Items: []agentcoretypes.GatewaySummary{{GatewayId: aws.String("gw-abc")}}},
		},
		getErr: errors.New("ThrottlingException"),
	}
	got, err := listAgentCoreGateways(context.Background(), client)
	require.NoError(t, err, "a single GetGateway failure must not abort the whole listing")
	require.Len(t, got, 1, "the gateway is still returned despite the failed ARN lookup")
	assert.Equal(t, "gw-abc", got[0].GatewayID)
	assert.Empty(t, got[0].GatewayArn, "a failed GetGateway leaves the ARN empty rather than failing the panel")
}

// TestInspectAgentCore_GetMetricsRoutesToMetricsPackage — get-metrics
// short-circuits to the metrics-package sentinel.
func TestInspectAgentCore_GetMetricsRoutesToMetricsPackage(t *testing.T) {
	t.Parallel()
	_, err := inspectAgentCore(context.Background(), aws.Config{Region: "us-east-1"}, "get-metrics", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUseMetricsPackage)
	assert.Contains(t, err.Error(), "agentcore")
}

func TestInspectAgentCore_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectAgentCore(context.Background(), aws.Config{Region: "us-east-1"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agentcore")
	assert.Contains(t, err.Error(), "no-such-action")
}
