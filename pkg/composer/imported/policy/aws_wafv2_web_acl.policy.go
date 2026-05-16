package policy

// awsWAFv2WebACLPolicy curates Layer 2 for `aws_wafv2_web_acl`.
// Cloud-control-routed enrichment already produces typed Attrs; this
// map adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A WAFv2 web ACL is the top-level firewall (vs. the existing
// `aws_wafv2_web_acl_association` which is the binding row from an
// ALB / API Gateway / CloudFront onto an ACL). Identity is (id, arn,
// name, scope). `scope` is REGIONAL | CLOUDFRONT and pinned at
// create. `default_action` (allow | block) gates fallthrough
// behavior — silently flipping is high-signal security drift.
// `rule` is the load-bearing rules surface — whole-list compare so
// any out-of-band rule add / remove / reorder is one diff entry.
//
// Drift bundle 9 (#482): scalars use DriftSemanticExact; the rule
// set, token_domains, and the nested config / response-body blocks
// compare WholeList. Per-rule sub-fields are not curated at this
// layer — the rule list is treated as an opaque whole, and the diff
// projection layer is responsible for per-rule rendering.
var awsWAFv2WebACLPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"capacity": {
		// Computed WCU consumption (function of rule complexity).
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"lock_token": {
		// Optimistic-concurrency token from the WAF API.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"application_integration_url": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — scope pin (REGIONAL vs CLOUDFRONT) ---------------------
	"scope": {
		// Pinned at create — flipping requires destroy/recreate.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — load-bearing security knobs ---------------------------
	"default_action": {
		// allow | block — silently flipping is the most security-
		// critical drift on this resource.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"rule": {
		// The rules list. Whole-list compare so any add/remove/reorder
		// is one diff entry. Per-rule sub-field rendering is the diff
		// projection layer's responsibility.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"rule_json": {
		// Alternative JSON-blob rule specification.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"visibility_config": {
		// CloudWatch metric + sampled-requests opt-in.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticWholeList,
	},
	"captcha_config": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"challenge_config": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"association_config": {
		// Per-attached-resource body-size + content-type knobs.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"custom_response_body": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"token_domains": {
		// Domains allowed in the WAF token cookie scope.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"description": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_wafv2_web_acl", awsWAFv2WebACLPolicy)
}
