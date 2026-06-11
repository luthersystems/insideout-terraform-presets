package policy

// awsBedrockagentAgentAliasPolicy curates Layer 2 for
// `aws_bedrockagent_agent_alias`.
//
// #787 backfill for the Bedrock Agents stack (#762 / #776). An alias is
// the stable pointer applications invoke instead of a raw agent version
// — analogous to a Lambda alias. The load-bearing surfaces are the
// parent agent binding (agent_id, replace-on-change) and the
// routing_configuration that pins which agent version the alias resolves
// to. A silent routing change is exactly what "production quietly serves
// a different agent version" looks like.
var awsBedrockagentAgentAliasPolicy = Map{
	// Identity ----------------------------------------------------------
	"agent_alias_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"agent_alias_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"agent_alias_name": {
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

	// Routing — which version the alias resolves to ------------------
	"routing_configuration": {
		// agent_version / provisioned_throughput per route. A silent
		// change serves a different agent version in production.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_bedrockagent_agent_alias", awsBedrockagentAgentAliasPolicy)
}
