package imported

// EnrichmentStatus reports whether the per-type AttributeEnricher fully
// populated a resource's Attrs payload. Empty string is the implicit
// "unknown" state — used at pre-enrich call sites and for resource types
// that have no registered enricher (Identity-only IRs). Downstream
// consumers (the Reliable importer wizard, future interactive-agent management views)
// read this to surface partial-data warnings without grep-ing the
// enricher's warn log; see issue #471 for the live-verification context
// that motivated adding a typed signal.
type EnrichmentStatus string

const (
	// EnrichmentStatusUnknown is the zero / not-yet-enriched state. The
	// enricher orchestrator never writes this value — it represents
	// "no enrich pass has touched this IR yet" (pre-enrich call sites,
	// or resource types without a registered AttributeEnricher).
	// Downstream consumers should treat it equivalently to a bare empty
	// string per the JSON `omitempty` tag.
	EnrichmentStatusUnknown EnrichmentStatus = ""
	// EnrichmentStatusFull indicates the enricher populated every
	// attribute it knows how to fetch. The IR's Attrs is authoritative.
	EnrichmentStatusFull EnrichmentStatus = "full"
	// EnrichmentStatusPartial indicates the enricher populated some
	// attributes but at least one fetch failed. Reserved for future
	// multi-call enrichers (e.g. bucket-ACL-plus-policy); current
	// per-type enrichers marshal Attrs atomically and so never set
	// Partial. TODO(#471): wire this when a multi-call enricher lands.
	EnrichmentStatusPartial EnrichmentStatus = "partial"
	// EnrichmentStatusFailed indicates no attributes could be populated
	// (client unavailable, name underivable, fetch error, marshal
	// error). The IR's Attrs is empty; consumers should treat it as
	// Identity-only and surface the EnrichErrors strings for triage.
	EnrichmentStatusFailed EnrichmentStatus = "failed"
)

// ResourceIdentity separates Terraform's stable address from cloud-side
// correlation identifiers. Address is immutable after import; renaming is a
// future explicit migration operation using `moved {}` blocks, not an ordinary
// edit. See docs/managed-resource-tiers.md lines 456-475 for the canonical
// shape and lines 517-544 for the address generation algorithm.
//
// Use ImportID and ProviderIdentity for Terraform import; use NativeIDs for
// lookup, dedupe, and cross-reference resolution.
type ResourceIdentity struct {
	// Cloud is the platform identifier: "aws" or "gcp".
	Cloud string `json:"cloud,omitempty"`
	// Type is the Terraform resource type, e.g. "aws_sqs_queue".
	Type string `json:"type,omitempty"`
	// Address is the immutable Terraform resource address (e.g.
	// "aws_sqs_queue.dlq"). Generated once via GenerateAddress and frozen.
	//
	// The label portion (after the dot) is the sanitized form of
	// NameHint per address.go::normalizeLabel: lowercase ASCII,
	// `[^a-z0-9_]` collapsed to `_`, repeated `_` merged, leading
	// non-letter prefixed with `r_`, capped to maxLabelLen. A bucket
	// named `b9043cd2-tfstate` therefore appears here as
	// `google_storage_bucket.b9043cd2_tfstate`. UI consumers that
	// surface a user-readable name should display NameHint alongside
	// (or instead of) Address when the two differ, and use Address
	// only for the TF-state/import-block form.
	Address string `json:"address,omitempty"`
	// NameHint is the original human-readable name source preserved for
	// audit and display. May contain characters that are illegal in a
	// Terraform label (hyphens, uppercase, dots) — Address holds the
	// sanitized form. See the Address field comment for the
	// relationship.
	NameHint string `json:"name_hint,omitempty"`
	// ParentAddress is the Terraform Address of the parent resource
	// instance this resource is a child of, when that parent is present
	// in the same discovery result. It is the per-instance counterpart of
	// the type-level pkg/imported/labels parentTfType registry: that map
	// answers "aws_s3_bucket_versioning is scoped to aws_s3_bucket"; this
	// field answers "this versioning instance belongs to that specific
	// bucket". Empty for resources with no parent, and for children whose
	// parent instance was not discovered in this run (no dangling
	// reference). Resolved post-discovery by joining the child's
	// foreign-key identifier against the discovered set — see
	// cmd/insideout-import/awsdiscover's parent resolver. Downstream
	// consumers (reliable's reverse-Terraform import wizard, reliable#1617)
	// use it to collapse child instance rows under their parent instance.
	ParentAddress string `json:"parent_address,omitempty"`
	// ProviderConfig identifies the provider alias used by emitted HCL,
	// e.g. "aws.imported" or "google.imported".
	ProviderConfig string `json:"provider_config,omitempty"`
	// ProviderSource is the registry source, e.g.
	// "registry.terraform.io/hashicorp/aws".
	ProviderSource string `json:"provider_source,omitempty"`
	// ProviderVersion is the exact provider version used at import time.
	ProviderVersion string `json:"provider_version,omitempty"`
	// SchemaVersion is the generated-schema / codegen version that produced
	// any associated typed Attrs. Empty for opaque-bag-only carriers.
	SchemaVersion string `json:"schema_version,omitempty"`

	// AccountID is the AWS account ID when applicable.
	AccountID string `json:"account_id,omitempty"`
	// ProjectID is the GCP project ID when applicable.
	ProjectID string `json:"project_id,omitempty"`
	// Region is the AWS region or GCP region when applicable.
	Region string `json:"region,omitempty"`
	// Location is the GCP zone/location when region is not enough.
	Location string `json:"location,omitempty"`
	// ImportID is the provider import ID: URL, ARN, name, self-link, etc.
	ImportID string `json:"import_id,omitempty"`
	// ProviderIdentity is the Terraform identity object when supported.
	ProviderIdentity map[string]string `json:"provider_identity,omitempty"`
	// NativeIDs holds cloud-side identifiers (arn, url, self_link, name).
	NativeIDs map[string]string `json:"native_ids,omitempty"`
	// Tags is the cloud-side tag (AWS) or label (GCP) map captured at
	// discover time. Empty map (not nil) when the resource genuinely has
	// no tags; nil when the discoverer didn't fetch tags. Downstream
	// consumers (#291 tag selectors, #289 gap-#6 DiscoverySummary.byTag)
	// rely on the nil-vs-empty distinction.
	Tags map[string]string `json:"tags,omitempty"`

	// ServiceManagedBy carries the cloud-side "managed by" principal for a
	// resource that an AWS/GCP service owns on the customer's behalf, e.g.
	// an EventBridge rule's ManagedBy ("autoscaling.amazonaws.com"). When
	// non-empty the instance is service-managed and CANNOT be managed by
	// Terraform: tag / create / delete operations are rejected by the
	// provider (EventBridge ManagedRuleException, #785). This is a
	// dedicated identity field rather than an Attrs/Tags entry so the
	// signal survives the snapshot envelope round-trip and the
	// schema-filter that drops computed-only attributes — the discoverer
	// sets it from the type's ServiceManagedByFromProperties hook, and
	// both the importability classifier (UnimportableReason →
	// ReasonServiceManaged) and the composer provenance injector (skip +
	// weak-lock) read it. Generic by design: any type that surfaces a
	// service-owner marker can populate it. Empty for customer-owned
	// resources.
	ServiceManagedBy string `json:"service_managed_by,omitempty"`

	// EnrichmentStatus reports whether the per-type enricher fully
	// populated this resource's Attrs. Empty == not-yet-enriched (the
	// discoverer hasn't run the enrich pass, or no enricher is
	// registered for this type — Identity-only IR). Set by the
	// EnrichAttributes orchestrator; per-type enrichers do not write
	// this directly. Downstream consumers (Reliable wizard, future
	// management views) read this to surface partial-data warnings
	// without grep-ing the enricher's warn log. Added for issue #471.
	EnrichmentStatus EnrichmentStatus `json:"enrichment_status,omitempty"`
	// EnrichErrors carries the per-attribute (or per-pass) error
	// messages when EnrichmentStatus != Full. Nil when Full or Unknown.
	// Verbose enough for triage but PII-free — error messages from the
	// cloud SDK pass through verbatim (no auth tokens, no full URLs
	// beyond the resource identifier).
	EnrichErrors []string `json:"enrich_errors,omitempty"`
}
