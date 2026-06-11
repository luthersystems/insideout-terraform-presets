package policy

// awsBedrockagentcoreGatewayTargetPolicy curates Layer 2 for
// `aws_bedrockagentcore_gateway_target`.
//
// #795 backfill for the aws/agentcore_gateway preset (#763 / #794). A
// target turns a backend (a Lambda, an API Gateway stage, an OpenAPI /
// Smithy spec, or an MCP server) into an agent-callable tool exposed by
// its parent gateway. Unlike the gateway, the target carries NO tags
// attribute, so this policy has no tag entries (mirroring the untaggable
// aws_bedrockagent_data_source convention). The load-bearing drift
// surfaces are:
//
//   - gateway_identifier — the parent gateway this target attaches to
//     (replace-on-change wiring).
//   - target_configuration.mcp.lambda.lambda_arn — for the Lambda tool
//     shape this preset emits, the function the gateway invokes. A silent
//     rebind points the tool at different code.
//   - credential_provider_configuration — how the target authenticates to
//     its backend. The api_key / oauth provider_arn references are Secrets
//     Manager / identity-provider credential wiring (Security pillar,
//     Redacted so the ARN shows for context but is never a raw secret),
//     per the #791 credentials_secret_arn precedent. The gateway_iam_role
//     mode (use the gateway's own execution role) is an empty marker block
//     with no scalar fields to curate.
//
// The provider exposes a very deep target_configuration tree (an
// inline/S3 tool_schema for the Lambda shape, plus api_gateway / mcp_
// server / open_api_schema / smithy_model alternatives, each with their
// own nested config). Curation targets the parent binding, the Lambda
// target binding this preset uses, and the credential wiring; the deep
// per-shape schema trees are left to the conservative codegen-only
// default.
var awsBedrockagentcoreGatewayTargetPolicy = Map{
	// Identity ----------------------------------------------------------
	"target_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent gateway ----------------------------------------
	"gateway_identifier": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tool backend — what the gateway invokes ------------------------
	// For the Lambda tool shape this preset emits, lambda_arn is the
	// function the gateway calls; a silent rebind points the tool at
	// different code.
	"target_configuration.mcp.lambda.lambda_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Credential wiring — how the target authenticates to its backend -
	// The api_key / oauth provider_arn point at Secrets Manager / OAuth
	// credential providers. A silent rebind points the tool at different
	// credentials (and thus a potentially different backend tenant).
	// Security wiring, Redacted so the ARN is shown for context but never
	// treated as a raw secret value (per the #791 credentials_secret_arn
	// precedent).
	"credential_provider_configuration.api_key.provider_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},
	"credential_provider_configuration.oauth.provider_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_bedrockagentcore_gateway_target", awsBedrockagentcoreGatewayTargetPolicy)
}
