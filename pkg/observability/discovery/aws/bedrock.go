// Bedrock service inspector.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go (bedrock:1251)
// plus the discovery helpers (discoverBedrockKnowledgeBases:1297,
// discoverBedrockAgents:1313, tagFilterBedrockResources:1325,
// lookupBedrockIAMRole:1367, discoverBedrockGuardrails:1389).
//
// Bedrock is split across two SDKs:
//
//   - bedrockagent owns KnowledgeBases, Agents, and ListTagsForResource
//     for those (tags returned as map[string]string — "map" format).
//   - bedrock (runtime) owns Guardrails and ListTagsForResource for
//     them (tags returned as []bedrocktypes.Tag — kv format).
//
// On top of that, the current preset can produce a "successful deploy"
// where ListKnowledgeBases returns nothing — the only resource it always
// creates is the `${project}-bedrock-role` IAM role. Without the IAM
// fallback, that deploy looks like a drift miss. Hence the
// `lookupBedrockIAMRole` synthetic resource path (#1018).

package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// bedrockAgentAPI covers the bedrockagent SDK subset we use. Mirrors
// The InsideOut backend's bedrockAgentAPI (aws_inspect.go:1213).
type bedrockAgentAPI interface {
	ListKnowledgeBases(ctx context.Context, in *bedrockagent.ListKnowledgeBasesInput, opts ...func(*bedrockagent.Options)) (*bedrockagent.ListKnowledgeBasesOutput, error)
	ListAgents(ctx context.Context, in *bedrockagent.ListAgentsInput, opts ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentsOutput, error)
	ListTagsForResource(ctx context.Context, in *bedrockagent.ListTagsForResourceInput, opts ...func(*bedrockagent.Options)) (*bedrockagent.ListTagsForResourceOutput, error)
	GetKnowledgeBase(ctx context.Context, in *bedrockagent.GetKnowledgeBaseInput, opts ...func(*bedrockagent.Options)) (*bedrockagent.GetKnowledgeBaseOutput, error)
}

// bedrockAPI covers the subset of the bedrock (runtime) SDK used for
// guardrail discovery. Mirrors the InsideOut backend's bedrockAPI (aws_inspect.go:1223).
type bedrockAPI interface {
	ListGuardrails(ctx context.Context, in *bedrock.ListGuardrailsInput, opts ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error)
	ListTagsForResource(ctx context.Context, in *bedrock.ListTagsForResourceInput, opts ...func(*bedrock.Options)) (*bedrock.ListTagsForResourceOutput, error)
}

// iamGetRoleAPI is the one-method IAM seam used by the IAM-role
// fallback. Kept tiny so tests can fake {role found, NoSuchEntity,
// generic error} without dragging in the full IAM SDK.
//
// Mirrors the InsideOut backend's iamGetRoleAPI (aws_inspect.go:1239).
type iamGetRoleAPI interface {
	GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error)
}

func inspectBedrock(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	bedrockAgentClient := bedrockagent.NewFromConfig(cfg)
	iamClient := iam.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "list-knowledge-bases":
		return discoverBedrockKnowledgeBases(ctx, bedrockAgentClient, iamClient, project)
	case "list-agents":
		return discoverBedrockAgents(ctx, bedrockAgentClient, project)
	case "list-guardrails":
		// Guardrails live on bedrock (runtime), not bedrockagent. Tags
		// return as []bedrocktypes.Tag (kv shape) — distinct from the
		// map shape bedrockagent uses.
		bedrockClient := bedrock.NewFromConfig(cfg)
		return discoverBedrockGuardrails(ctx, bedrockClient, project)
	case "describe-knowledge-base":
		var filterMap map[string]string
		if filters != "" {
			_ = json.Unmarshal([]byte(filters), &filterMap)
		}
		kbID := filterMap["knowledgeBaseId"]
		if kbID == "" {
			return nil, fmt.Errorf("describe-knowledge-base requires knowledgeBaseId in filters")
		}
		out, err := bedrockAgentClient.GetKnowledgeBase(ctx, &bedrockagent.GetKnowledgeBaseInput{
			KnowledgeBaseId: aws.String(kbID),
		})
		if err != nil {
			return nil, err
		}
		return out.KnowledgeBase, nil
	case "get-metrics":
		return metricsRouted("bedrock")
	default:
		return nil, unsupportedActionError("bedrock", action)
	}
}

// discoverBedrockKnowledgeBases enumerates KBs and tag-matches by
// Project. When zero KBs match (the common case for the current preset,
// which provisions only the IAM role), falls back to checking for the
// `${project}-bedrock-role` IAM role so a successful deploy still
// produces a non-empty result and drift detection stays correct.
//
// Mirrors the InsideOut backend's discoverBedrockKnowledgeBases (aws_inspect.go:1297).
func discoverBedrockKnowledgeBases(ctx context.Context, client bedrockAgentAPI, iamClient iamGetRoleAPI, project string) (any, error) {
	out, err := client.ListKnowledgeBases(ctx, &bedrockagent.ListKnowledgeBasesInput{})
	if err != nil {
		return []any{}, err
	}
	matched := tagFilterBedrockResources(ctx, client, toSliceOfMaps(out.KnowledgeBaseSummaries), project)
	if len(matched) > 0 || project == "" {
		return matched, nil
	}
	if role, ok := lookupBedrockIAMRole(ctx, iamClient, project); ok {
		return []map[string]any{role}, nil
	}
	return matched, nil
}

func discoverBedrockAgents(ctx context.Context, client bedrockAgentAPI, project string) (any, error) {
	out, err := client.ListAgents(ctx, &bedrockagent.ListAgentsInput{})
	if err != nil {
		return []any{}, err
	}
	return tagFilterBedrockResources(ctx, client, toSliceOfMaps(out.AgentSummaries), project), nil
}

// tagFilterBedrockResources fans ListTagsForResource over a slice of
// JSON-shaped Bedrock resources and keeps only those tagged
// Project=<project>. Per-resource tag-fetch errors log+skip.
//
// The bedrockagent SDK returns Tags as map[string]string ("map" format).
//
// Mirrors the InsideOut backend's tagFilterBedrockResources (aws_inspect.go:1325).
func tagFilterBedrockResources(ctx context.Context, client bedrockAgentAPI, resources []map[string]any, project string) []map[string]any {
	if project == "" {
		return resources
	}
	matched := []map[string]any{}
	for _, r := range resources {
		arn := firstNonEmptyString(
			getString(r, "KnowledgeBaseArn"),
			getString(r, "AgentArn"),
			getString(r, "GuardrailArn"),
			getString(r, "Arn"),
		)
		if arn == "" {
			continue
		}
		tagsOut, err := client.ListTagsForResource(ctx, &bedrockagent.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
		if err != nil {
			log.Printf("[aws-inspect] bedrock ListTagsForResource %s: %v (skipping)", arn, err)
			continue
		}
		tagsAny := make(map[string]any, len(tagsOut.Tags))
		for k, v := range tagsOut.Tags {
			tagsAny[k] = v
		}
		if filter.MatchesTag(tagsAny, project, filter.FormatMap) {
			matched = append(matched, r)
		}
	}
	return matched
}

// lookupBedrockIAMRole returns a synthetic resource map for the first
// preset-provisioned Bedrock IAM role found. Probes
// `${project}-bedrock-role` (KB role) then `${project}-bedrock-logging-role`
// (added by preset PR #80 for invocation logging). Returns false if
// neither exists.
//
// Mirrors the InsideOut backend's lookupBedrockIAMRole (aws_inspect.go:1367).
func lookupBedrockIAMRole(ctx context.Context, iamClient iamGetRoleAPI, project string) (map[string]any, bool) {
	for _, suffix := range []string{"-bedrock-role", "-bedrock-logging-role"} {
		roleName := project + suffix
		out, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
		if err != nil {
			// NoSuchEntity is the expected case when the role isn't
			// provisioned; log at debug-equivalent volume and try next.
			log.Printf("[aws-inspect] bedrock iam.GetRole %s: %v", roleName, err)
			continue
		}
		return map[string]any{
			"Kind":     "IAMRole",
			"RoleName": aws.ToString(out.Role.RoleName),
			"Arn":      aws.ToString(out.Role.Arn),
		}, true
	}
	return nil, false
}

// discoverBedrockGuardrails enumerates guardrails via the bedrock
// (runtime) SDK and tag-matches by Project. The bedrock SDK returns
// tags as kv-shape ([]bedrocktypes.Tag), distinct from bedrockagent's
// map[string]string.
//
// Mirrors the InsideOut backend's discoverBedrockGuardrails (aws_inspect.go:1389).
func discoverBedrockGuardrails(ctx context.Context, client bedrockAPI, project string) (any, error) {
	out, err := client.ListGuardrails(ctx, &bedrock.ListGuardrailsInput{})
	if err != nil {
		return []any{}, err
	}
	resources := toSliceOfMaps(out.Guardrails)
	if project == "" {
		return resources, nil
	}
	matched := []map[string]any{}
	for _, r := range resources {
		arn := firstNonEmptyString(
			getString(r, "Arn"),
			getString(r, "GuardrailArn"),
		)
		if arn == "" {
			continue
		}
		tagsOut, tagErr := client.ListTagsForResource(ctx, &bedrock.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
		if tagErr != nil {
			log.Printf("[aws-inspect] bedrock ListTagsForResource %s: %v (skipping)", arn, tagErr)
			continue
		}
		if hasProjectTagBedrock(tagsOut.Tags, project) {
			matched = append(matched, r)
		}
	}
	return matched, nil
}

// hasProjectTagBedrock reports whether the bedrock tag list (kv shape)
// has a Project=<project> entry.
func hasProjectTagBedrock(tags []bedrocktypes.Tag, project string) bool {
	for _, t := range tags {
		if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
			return true
		}
	}
	return false
}
