package policy

// awsBedrockagentcoreGatewayPolicy curates Layer 2 for
// `aws_bedrockagentcore_gateway`.
//
// #795 backfill for the aws/agentcore_gateway preset (#763 / #794). A
// gateway is an AgentCore MCP/tool endpoint: callers (agents / MCP
// clients) connect to its URL, authenticate against an inbound JWT
// authorizer, and the gateway assumes role_arn to invoke its targets.
// The load-bearing drift surfaces are:
//
//   - role_arn — the IAM identity the gateway assumes to invoke its
//     targets. A silent rebind re-purposes the gateway's privilege.
//     Curated as Security wiring (the canonical role_arn classification
//     across this corpus); the issue's "identity" framing refers to the
//     who-it-acts-as concern, which the Security pillar already carries —
//     the axis itself is Wiring because role_arn is a cross-reference to
//     another managed resource, not the gateway's own identity.
//   - the CUSTOM_JWT authorizer block — discovery_url (the OIDC issuer
//     whose tokens are trusted) and the allowed_clients / allowed_audience
//     / allowed_scopes allowlists. This is the inbound auth surface: a
//     silent edit changes who can call the gateway and with what token.
//     Classified conservatively as Security wiring per the #791 auth-
//     surface precedent (the data-source credentials_secret_arn wiring) —
//     RelationshipOnly so it is never scalar-edited through chat.
//   - protocol_type / authorizer_type — the gateway's wire contract and
//     auth mode; both are replace-shaped enums whose silent change
//     reshapes the endpoint.
//   - kms_key_arn — at-rest protection of the gateway's stored config.
//
// The provider also exposes an interceptor_configuration tree (request/
// response Lambda interception) and the MCP protocol tuning knobs; the
// interceptor Lambda binding is curated (it can observe/mutate every
// request) while the deeper MCP search/version knobs are left to the
// conservative codegen-only default.
var awsBedrockagentcoreGatewayPolicy = Map{
	// Identity ----------------------------------------------------------
	"gateway_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"gateway_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"gateway_url": {
		// The endpoint agents connect to — computed, stable identity.
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

	// Wiring — target-invocation IAM identity ------------------------
	"role_arn": {
		// Silent rebind re-purposes the gateway's invocation privilege.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Wire / auth contract — replace-shaped enums --------------------
	"authorizer_type": {
		// e.g. CUSTOM_JWT — the gateway's inbound auth mode.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"protocol_type": {
		// e.g. MCP — the gateway's wire protocol.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Inbound auth surface — who may call the gateway ----------------
	// The CUSTOM_JWT authorizer trusts tokens from discovery_url's OIDC
	// issuer and (optionally) restricts them to the allowed_clients /
	// allowed_audience / allowed_scopes allowlists. A silent edit to any
	// of these changes who can invoke the gateway, so they are curated
	// conservatively as Security wiring per the #791 auth-surface
	// precedent. discovery_url is required; the allowlists are sets.
	"authorizer_configuration.custom_jwt_authorizer.discovery_url": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"authorizer_configuration.custom_jwt_authorizer.allowed_clients": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"authorizer_configuration.custom_jwt_authorizer.allowed_audience": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	"authorizer_configuration.custom_jwt_authorizer.allowed_scopes": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Interceptor — request/response Lambda in the call path ---------
	// An interceptor Lambda observes (and can mutate) every request the
	// gateway proxies; a silent rebind inserts different code into the
	// hot path. Security wiring.
	"interceptor_configuration.interceptor.lambda.arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Encryption — at-rest protection of the gateway config ----------
	"kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_bedrockagentcore_gateway", awsBedrockagentcoreGatewayPolicy)
}
