package policy

// awsBedrockagentAgentActionGroupPolicy curates Layer 2 for
// `aws_bedrockagent_agent_action_group`.
//
// #787 backfill for the Bedrock Agents stack (#762 / #776). An action
// group extends an agent with callable tools: it binds an executor
// (a Lambda the agent invokes) and an API/function schema describing the
// tool surface. The high-value drift surfaces are:
//
//   - agent_id / agent_version — the parent agent this group attaches to
//     (replace-on-change wiring).
//   - action_group_executor.lambda — the Lambda ARN the agent invokes.
//     A silent rebind re-routes the agent's tool calls to a different
//     function (code-execution shaped).
//   - action_group_state — Enabled | Disabled. Silently disabling a
//     group removes a capability; silently enabling one exposes a tool.
//   - the API schema (inline payload or the S3-hosted document) — the
//     contract the agent calls against.
var awsBedrockagentAgentActionGroupPolicy = Map{
	// Identity ----------------------------------------------------------
	"action_group_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"action_group_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent agent ------------------------------------------
	"agent_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"agent_version": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — capability gate ---------------------------------------
	"action_group_state": {
		// Enabled | Disabled — silently toggling a tool capability.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Executor — what code the agent invokes -------------------------
	"action_group_executor.lambda": {
		// Lambda ARN the agent calls. A silent rebind re-routes tool calls.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"action_group_executor.custom_control": {
		// RETURN_CONTROL — the non-Lambda executor mode. Flipping between
		// Lambda execution and return-control changes who runs the tool.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// API schema — the tool contract ---------------------------------
	"api_schema.payload": {
		// Inline OpenAPI schema describing the callable tool surface. An
		// out-of-band edit changes the contract the agent calls against.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"api_schema.s3.s3_bucket_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"api_schema.s3.s3_object_key": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Function schema — the alternative (non-OpenAPI) tool contract ---
	// Curated whole: a silent edit to the declared functions / parameters
	// re-shapes the tool surface the agent can invoke.
	"function_schema": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_bedrockagent_agent_action_group", awsBedrockagentAgentActionGroupPolicy)
}
