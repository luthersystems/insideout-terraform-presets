package policy

// awsBedrockGuardrailPolicy curates Layer 2 for `aws_bedrock_guardrail`.
//
// Bundle E (#482): hand-rolled Bucket-C coverage push lifts AWS to 98%
// Enrichable. The guardrail TF surface enumerates a content / topic /
// word / sensitive-information policy family — each is a nested block
// list of filter declarations. Per the dynamodb_table precedent the
// nested blocks are addressed as dotted leaves, each a scalar; per-
// leaf Exact compare is the right granularity.
//
// All curated leaves are scalar (strings, floats, enums) — DriftSemanticExact
// is the meaningful comparison. Tag bags stay DriftSemanticNone
// (tagPolicy() zero value).
var awsBedrockGuardrailPolicy = Map{
	// Identity ---------------------------------------------------------
	"guardrail_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"guardrail_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Guardrail name is the user-visible identifier — recreate on rename.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"status": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"created_at": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring (KMS) -----------------------------------------------------
	"kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — top-level messaging knobs -------------------------------
	"blocked_input_messaging": {
		// The default refusal text shown for blocked input — operator-
		// tunable string the interactive agent can update directly.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"blocked_outputs_messaging": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Content policy filters (per-category strength knobs) --------------
	// Each filter declares a category (HATE, VIOLENCE, …) and two
	// strength dials. Strength changes are reversible, but they're
	// security-sensitive so RequiresApproval gates the agent.
	"content_policy_config.filters_config.input_strength": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"content_policy_config.filters_config.output_strength": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"content_policy_config.filters_config.type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Contextual grounding filters -------------------------------------
	"contextual_grounding_policy_config.filters_config.threshold": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"contextual_grounding_policy_config.filters_config.type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Sensitive-information policy -------------------------------------
	// PII entity blocks: enumerate the entity type (EMAIL, PHONE, …) and
	// an action verb (BLOCK / ANONYMIZE). Type is identity; action is
	// the operator-visible knob.
	"sensitive_information_policy_config.pii_entities_config.type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"sensitive_information_policy_config.pii_entities_config.action": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"sensitive_information_policy_config.regexes_config.name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"sensitive_information_policy_config.regexes_config.description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"sensitive_information_policy_config.regexes_config.pattern": {
		// A regex tweak can dramatically change blocked-coverage —
		// approval-gated.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"sensitive_information_policy_config.regexes_config.action": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Topic policy ----------------------------------------------------
	"topic_policy_config.topics_config.name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"topic_policy_config.topics_config.definition": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"topic_policy_config.topics_config.type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"topic_policy_config.topics_config.examples": {
		// List of example strings — order may be provider-stable but
		// not semantically meaningful; whole-list compare matches the
		// dynamodb non_key_attributes precedent.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Word policy -----------------------------------------------------
	"word_policy_config.words_config.text": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"word_policy_config.managed_word_lists_config.type": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags (system-managed bag) ---------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_bedrock_guardrail", awsBedrockGuardrailPolicy)
}
