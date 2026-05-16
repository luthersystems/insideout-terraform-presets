package policy

// awsCloudwatchLogStreamPolicy curates Layer 2 for
// `aws_cloudwatch_log_stream`. Cloud-control-routed enrichment already
// produces typed Attrs; this map adds the curated surface to flip the
// type from Enrichable to DriftDetectable.
//
// A CloudWatch log stream is the append-only event log inside a parent
// log group. Identity is (name, log_group_name, arn). The shape is
// extremely flat — there is no editable tuning surface beyond the parent
// pointer. Drift on log_group_name means the stream was re-attached to a
// different group, which is structural drift (the resource is effectively
// a different stream).
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact. No tag surface
// — CloudWatch log streams are not tagged.
var awsCloudwatchLogStreamPolicy = Map{
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
		// Stream name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent log group ---------------------------------------
	"log_group_name": {
		// Pointer to the parent aws_cloudwatch_log_group. Pinned at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_cloudwatch_log_stream", awsCloudwatchLogStreamPolicy)
}
