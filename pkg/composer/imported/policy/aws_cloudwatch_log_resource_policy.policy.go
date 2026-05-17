package policy

// awsCloudwatchLogResourcePolicyPolicy curates Layer 2 for
// `aws_cloudwatch_log_resource_policy`.
//
// A CloudWatch Logs resource policy is an IAM-style document attached
// at the log-group scope (vs. account-wide IAM policies). It controls
// which principals — typically other AWS services like Route 53,
// CloudFront, or third-party SaaS — can call PutLogEvents on the
// account's log groups matched by the policy's Resource ARN pattern.
// Identity is (policy_name). The `policy_document` JSON is the
// security-critical surface: drift means out-of-band access-grant
// changes.
//
// Drift bundle 12 (#482): scalars use DriftSemanticExact. No tag
// surface — resource policies are not tagged.
var awsCloudwatchLogResourcePolicyPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"policy_name": {
		// Account-scoped name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Security-critical payload ---------------------------------------
	"policy_document": {
		// IAM policy JSON. Who can PutLogEvents on which log groups.
		// Drift = out-of-band access-grant changes.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cloudwatch_log_resource_policy", awsCloudwatchLogResourcePolicyPolicy)
}
