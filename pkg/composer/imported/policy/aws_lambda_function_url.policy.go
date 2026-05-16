package policy

// awsLambdaFunctionURLPolicy curates Layer 2 for `aws_lambda_function_url`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Lambda Function URL is the standalone HTTPS endpoint pinned to a
// function (versioned via `qualifier`). Identity is (id, function_arn,
// url_id) and the public URL itself is computed. The security-critical
// knob is `authorization_type` (NONE means publicly-callable, AWS_IAM
// means SigV4-gated) — flipping it from AWS_IAM to NONE is the canonical
// compliance regression. The nested `cors` block is left uncurated —
// block-level drift is a follow-up.
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact. Timeouts use
// timeoutsPolicy(). No tag surface — function URLs don't carry user tags.
var awsLambdaFunctionURLPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"function_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"function_url": {
		// Public HTTPS endpoint URL — computed.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"url_id": {
		// Stable URL ID portion of the function URL hostname.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — target function + version ------------------------------
	"function_name": {
		// Name of the Lambda function the URL points at. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"qualifier": {
		// Lambda alias or version. Pinned at create.
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Security / behavior --------------------------------------------
	"authorization_type": {
		// NONE | AWS_IAM. NONE means the URL is publicly callable —
		// flipping AWS_IAM → NONE is the canonical compliance regression.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"invoke_mode": {
		// BUFFERED | RESPONSE_STREAM.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_lambda_function_url", awsLambdaFunctionURLPolicy)
}
