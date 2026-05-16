package policy

// googleStorageBucketObjectPolicy curates Layer 2 for
// `google_storage_bucket_object`.
//
// Companion to googleStorageBucketPolicy: the parent bucket holds
// per-bucket settings (versioning, lifecycle, CORS); this object
// resource holds per-object HTTP metadata plus the Sensitive body
// material (`content`).
//
// The body is not curated here — the enricher never populates it
// (Storage.Objects.Get returns metadata only, body needs a separate
// `.Download()` call surfacing an io.Reader), and the carrier
// escalates `content` via lifecycle.ignore_changes in
// genconfig.cleanup. Drift on `content` would be a false positive
// every refresh, so it stays DriftSemanticNone (the bag default).
var googleStorageBucketObjectPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"output_name": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"media_link": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent bucket (the object belongs to exactly one bucket;
	// rewiring is a recreate) and KMS encryption key.
	"bucket": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_key_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — HTTP metadata and storage class. content_type and
	// storage_class are the most-edited knobs.
	"content_type": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"storage_class": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"cache_control": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"content_disposition": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"content_encoding": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"content_language": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"event_based_hold": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"temporary_hold": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"detect_md5hash": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
	},

	// Observability — content digests and generation are Computed and
	// read-only. Engine has no separate Observability role today (#491),
	// so we re-use RoleIdentity with EditNever + SummaryVisible.
	"crc32c": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
	},
	"md5hash": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
	},
	"generation": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
	},

	// Body — see header. Untracked: Get doesn't return payload, drift
	// comparison would always fire, and exposing the material would
	// defeat the carrier's ignore_changes escalation.
	"content": tagPolicy(),
	"source":  tagPolicy(),

	// Customer-supplied encryption (CSEK) keys are Sensitive material —
	// follow the same Hidden + SystemOnly + Redacted posture as the
	// IDP client_secret.
	"customer_encryption.encryption_algorithm": tagPolicy(),
	"customer_encryption.encryption_key":       tagPolicy(),

	// Retention block — when set, the lock-until time is operationally
	// meaningful (compliance hold).
	"retention.mode": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"retention.retain_until_time": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Metadata bag — user-defined key/value pairs, system-owned posture.
	"metadata": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_storage_bucket_object", googleStorageBucketObjectPolicy)
}
