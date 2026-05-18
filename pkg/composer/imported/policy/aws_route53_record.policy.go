package policy

// awsRoute53RecordPolicy curates Layer 2 for `aws_route53_record`. Record
// identity is (zone_id, name, type[, set_identifier]); the resource ID
// is the upstream API's composite of those four fields. `records` carries
// the actual rdata payload (one or more A / AAAA / TXT / CNAME values etc.)
// and is compared as a WholeList — order-insensitive in DNS semantics but
// stored as an ordered list by the provider, so whole-list compare reports
// a missing or extra rdata entry as one diff event instead of N.
//
// Routing-policy sub-blocks (`alias`, `weighted_*`, `failover_*`,
// `geolocation_*`, `geoproximity_*`, `latency_*`, `cidr_*`) are tuning
// scalars where present — every field is curated Exact, with the
// security/reliability pillar set per-block (alias targets are wiring,
// weighted/failover are reliability). Route53 records are not taggable,
// so there is no tag surface to curate.
//
// Bundle (#599): scalars use DriftSemanticExact; `records` and the
// per-block list-valued fields use DriftSemanticWholeList. `alias.name`
// + `alias.zone_id` are RoleWiring (cross-resource references to ALB /
// CloudFront / API Gateway). The codegen-only bucket entry — the live
// awsdiscover constructor still lacks an SDKLister for record sets.
var awsRoute53RecordPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// FQDN of the record. Pinned at create; provider replaces on change.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"type": {
		// A / AAAA / CNAME / TXT / MX / SRV / NS etc. Pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"zone_id": {
		// Hosted zone the record belongs to. Cross-resource wiring; pinned
		// at create.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"set_identifier": {
		// Tiebreaker for multiple records with the same name+type (used by
		// weighted / failover / latency / geolocation routing). Part of
		// identity in those modes.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"fqdn": {
		// Provider-emitted full name (name + zone DNS suffix).
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — TTL + rdata ---------------------------------------------
	"ttl": {
		// Seconds. Mutable in place.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"records": {
		// The rdata payload (A/AAAA/TXT/MX/etc. values). Order-insensitive
		// in DNS but stored as a list — whole-list compare emits one diff
		// for a missing-or-extra entry. Editing the rdata set is a real
		// traffic-routing change so it requires approval rather than ChatSafe.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"allow_overwrite": {
		// Permits TF to overwrite an existing record on create. Destructive
		// flag; system-owned.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditSystemOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"health_check_id": {
		// Optional Route53 health-check ID for failover routing. Wiring.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"multivalue_answer_routing_policy": {
		// Bool toggle for multivalue-answer policy.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Alias block — points at an AWS service endpoint instead of rdata.
	"alias.name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"alias.zone_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"alias.evaluate_target_health": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Weighted routing -------------------------------------------------
	"weighted_routing_policy.weight": {
		// Per-record weight; tuning a traffic split is a real change.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Failover routing --------------------------------------------------
	"failover_routing_policy.type": {
		// PRIMARY / SECONDARY.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Geolocation routing -----------------------------------------------
	"geolocation_routing_policy.continent": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"geolocation_routing_policy.country": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"geolocation_routing_policy.subdivision": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Geoproximity routing ----------------------------------------------
	"geoproximity_routing_policy.aws_region": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"geoproximity_routing_policy.bias": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"geoproximity_routing_policy.local_zone_group": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"geoproximity_routing_policy.coordinates.latitude": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"geoproximity_routing_policy.coordinates.longitude": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Latency routing ---------------------------------------------------
	"latency_routing_policy.region": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// CIDR routing ------------------------------------------------------
	"cidr_routing_policy.collection_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"cidr_routing_policy.location_name": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_route53_record", awsRoute53RecordPolicy)
}
