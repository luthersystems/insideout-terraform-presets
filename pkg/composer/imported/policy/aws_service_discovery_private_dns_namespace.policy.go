package policy

// awsServiceDiscoveryPrivateDNSNamespacePolicy curates Layer 2 for
// `aws_service_discovery_private_dns_namespace`.
//
// Bundle E (#482): hand-rolled Bucket-C coverage push lifts AWS to 98%
// Enrichable. The TF surface is small — name + vpc are immutable
// primary-key inputs, description is the only mutable knob, and
// arn / hosted_zone are computed identity fields. All curated leaves
// are scalar — DriftSemanticExact is the meaningful comparison. Tag
// bags stay DriftSemanticNone (tagPolicy() zero value).
var awsServiceDiscoveryPrivateDNSNamespacePolicy = Map{
	// Identity ---------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Namespace name + DNS suffix — recreate on rename per the
		// provider's ForceNew schema.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"hosted_zone": {
		// Route53 private hosted zone id — computed identity, mirrors
		// the discoverer's hosted_zone_id NativeID.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — VPC association -----------------------------------------
	"vpc": {
		// The VPC the namespace is bound to. The composer's graph
		// resolver owns the relationship; namespace recreates if the
		// VPC changes (per provider ForceNew).
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning -----------------------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags (system-managed bag) ----------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_service_discovery_private_dns_namespace", awsServiceDiscoveryPrivateDNSNamespacePolicy)
}
