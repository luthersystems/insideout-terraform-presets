package policy

// awsCloudwatchEventBusPolicy curates Layer 2 for `aws_cloudwatch_event_bus`
// (the TF canonical name; the underlying AWS service is now EventBridge).
//
// An event bus is the routing target for event_rule matches. Identity is
// (name, arn). The only operational knobs are:
//
//   - kms_key_identifier  — at-rest CMK for event payloads at rest
//   - event_source_name   — partner-event-source binding (pinned at create)
//
// Drift bundle 5 (#482): scalar attributes use DriftSemanticExact. The
// resource has no list-shaped attrs, so WholeList isn't applicable here;
// the bundle-wide ≥1 WholeList requirement is satisfied across the 10
// new policies by aws_cognito_user_pool_client (callback_urls etc.),
// aws_ecs_service (load_balancer / placement strategies), and others.
var awsCloudwatchEventBusPolicy = Map{
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

	// Wiring — partner event source + CMK ------------------------------
	"event_source_name": {
		// Partner SaaS event-source binding. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_key_identifier": {
		// CMK encrypting events at rest on the bus.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_cloudwatch_event_bus", awsCloudwatchEventBusPolicy)
}
