package policy

// awsBedrockagentAgentPolicy curates Layer 2 for
// `aws_bedrockagent_agent`.
//
// #787 backfill for the Bedrock Agents stack (#762 / #776). A Bedrock
// agent binds a foundation model, the IAM role it assumes at runtime,
// and the natural-language instruction that defines its behavior. The
// high-value drift surfaces are:
//
//   - agent_resource_role_arn — the identity the agent runtime assumes
//     to invoke the model and reach action-group lambdas / knowledge
//     bases. A silent rebind re-purposes the agent's IAM identity
//     (privilege-escalation shaped).
//   - foundation_model — which model the agent actually runs on. A
//     silent swap changes behavior and cost profile.
//   - instruction — the system prompt that governs what the agent does;
//     out-of-band edits are a behavioral / safety drift signal.
//   - guardrail_configuration — the content-safety guardrail binding;
//     silently dropping it removes the safety net.
//   - customer_encryption_key_arn — the CMK protecting the agent's
//     stored configuration.
var awsBedrockagentAgentPolicy = Map{
	// Identity ----------------------------------------------------------
	"agent_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"agent_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"agent_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"agent_version": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — runtime IAM identity + CMK ----------------------------
	"agent_resource_role_arn": {
		// Silent rebind re-purposes the agent runtime's IAM identity.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"customer_encryption_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — model + behavior + safety -----------------------------
	"foundation_model": {
		// Silent swap changes the agent's behavior and cost profile.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"instruction": {
		// The system prompt — out-of-band edits are behavioral drift.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"guardrail_configuration": {
		// Content-safety guardrail binding (guardrail_identifier +
		// version). Silently dropping it removes the safety net.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"idle_session_ttl_in_seconds": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_bedrockagent_agent", awsBedrockagentAgentPolicy)
}
