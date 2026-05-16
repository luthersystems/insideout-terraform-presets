package policy

// awsAutoscalingGroupTagPolicy curates Layer 2 for
// `aws_autoscaling_group_tag`.
//
// The TF resource binds one (key, value, propagate_at_launch) tag to
// one Auto Scaling Group — schema is the ASG name plus a `tag` nested
// block carrying `key`, `value`, and `propagate_at_launch`. The
// (asg_name, key) tuple is the identity; the rest is configuration.
//
// Curation: identity leaves use Exact drift semantics; the tag value
// is the lone editable field (operators routinely retag fleets out-
// of-band) and uses Exact too so unexpected mutations surface as
// drift. `propagate_at_launch` is structural — flipping it changes
// how new instances inherit the tag, so Exact equality is the right
// posture.
var awsAutoscalingGroupTagPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"autoscaling_group_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tag nested block — key is identity, value + propagate_at_launch
	// are editable configuration. The block itself is required by the
	// schema (`tag` is a singular nested block carrying the tag triple).
	"tag.key": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"tag.value": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"tag.propagate_at_launch": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_autoscaling_group_tag", awsAutoscalingGroupTagPolicy)
}
