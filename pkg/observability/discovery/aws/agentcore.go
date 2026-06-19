// AWS Bedrock AgentCore Gateway inspector (issue #763).
//
// Provides panel-default discovery for the aws/agentcore_gateway preset
// (a Bedrock AgentCore MCP/tool gateway). One list action plus the metrics
// passthrough:
//
//   - list-gateways — ListGateways returns []types.GatewaySummary, but the
//     summary carries only GatewayId, NOT the ARN. The AWS/Bedrock-AgentCore
//     CloudWatch namespace is keyed on the Resource dimension whose value is
//     the gateway ARN, so list-gateways resolves each id to its ARN via a
//     per-id GetGateway call (GetGatewayOutput.GatewayArn) and returns the
//     enriched summaries. This is the action metrics-discovery uses to
//     enumerate the Resource dimension values account-wide. No required
//     filter. Paginated via NextToken.
//   - get-metrics — routed to pkg/observability/metrics; AWS/Bedrock-AgentCore
//     emits CloudWatch metrics that the metrics package owns.
//
// Issue #255 contract: list-gateways uses nilSliceToEmpty so an empty AWS
// response marshals as `[]`, never `null`.

package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
)

// agentCoreClient is the narrowed SDK surface used by inspectAgentCore.
// Lets tests inject a fake without doing real AWS auth. ListGateways yields
// the id-only summaries; GetGateway resolves each id to its ARN (the Resource
// dimension value the AWS/Bedrock-AgentCore namespace is keyed on).
type agentCoreClient interface {
	ListGateways(ctx context.Context, params *bedrockagentcorecontrol.ListGatewaysInput, optFns ...func(*bedrockagentcorecontrol.Options)) (*bedrockagentcorecontrol.ListGatewaysOutput, error)
	GetGateway(ctx context.Context, params *bedrockagentcorecontrol.GetGatewayInput, optFns ...func(*bedrockagentcorecontrol.Options)) (*bedrockagentcorecontrol.GetGatewayOutput, error)
}

// AgentCoreGateway is the panel-default discovery record for a single
// AgentCore gateway. It augments the ListGateways summary (id + name) with
// the ARN resolved from GetGateway — the ARN is the Resource dimension value
// the AWS/Bedrock-AgentCore CloudWatch namespace is keyed on, so it is the
// load-bearing field for metrics-discovery.
type AgentCoreGateway struct {
	GatewayID   string `json:"gateway_id"`
	Name        string `json:"name,omitempty"`
	GatewayArn  string `json:"gateway_arn,omitempty"`
	Status      string `json:"status,omitempty"`
	Description string `json:"description,omitempty"`
}

func inspectAgentCore(ctx context.Context, cfg aws.Config, action, _ string) (any, error) {
	switch action {
	case "list-gateways":
		return listAgentCoreGateways(ctx, bedrockagentcorecontrol.NewFromConfig(cfg))
	case "get-metrics":
		// AgentCore gateways emit CloudWatch metrics under the
		// AWS/Bedrock-AgentCore namespace; the metrics fetch path owns those.
		// Route through metricsRouted so callers pivot to
		// pkg/observability/metrics.
		return metricsRouted("agentcore")
	default:
		return nil, unsupportedActionError("agentcore", action)
	}
}

// listAgentCoreGateways enumerates every gateway in the account+region
// (paginating via NextToken) and, for each, resolves the ARN with GetGateway —
// GatewaySummary carries only GatewayId, so the Resource-dimension ARN the
// AWS/Bedrock-AgentCore namespace is keyed on must be fetched per id. The
// returned slice is nil-normalized to [] (#255).
func listAgentCoreGateways(ctx context.Context, client agentCoreClient) ([]AgentCoreGateway, error) {
	gateways := []AgentCoreGateway{}

	var nextToken *string
	for {
		out, err := client.ListGateways(ctx, &bedrockagentcorecontrol.ListGatewaysInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}
		for _, s := range out.Items {
			id := aws.ToString(s.GatewayId)
			rec := AgentCoreGateway{
				GatewayID:   id,
				Name:        aws.ToString(s.Name),
				Status:      string(s.Status),
				Description: aws.ToString(s.Description),
			}
			// GatewaySummary has no ARN; resolve it (the Resource dimension
			// value) via GetGateway. A failed lookup leaves the ARN empty
			// rather than aborting the whole listing — the id-keyed record is
			// still useful for the panel, and one throttled/AccessDenied gateway
			// must not collapse the whole panel to the #255 empty state.
			if id != "" {
				g, gerr := client.GetGateway(ctx, &bedrockagentcorecontrol.GetGatewayInput{
					GatewayIdentifier: aws.String(id),
				})
				if gerr == nil {
					rec.GatewayArn = aws.ToString(g.GatewayArn)
				}
			}
			gateways = append(gateways, rec)
		}
		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	return nilSliceToEmpty(gateways), nil
}
