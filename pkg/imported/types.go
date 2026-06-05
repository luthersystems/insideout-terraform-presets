// Package imported is the cloud-agnostic registry of imported-resource
// concerns. It exposes the Provider interface every cloud implements,
// plus the small set of value types (Capabilities, FieldMismatch,
// Clients, DiscoverOpts) that ferry data across the boundary without
// dragging cloud-specific dependencies into callers.
//
// Architecture: this package defines the contract; per-cloud
// implementations live under pkg/imported/aws and pkg/imported/gcp.
// The dependency direction is one-way — per-cloud packages import
// pkg/imported, never the reverse — so the top-level package can be
// pulled in by consumers (e.g. luthersystems/reliable's importer
// wizard) without forcing them to install AWS or GCP SDKs they don't
// need. The Clients struct uses untyped any fields rather than typed
// cloud-specific pointers for the same reason; per-cloud Provider
// impls type-assert to their concrete shape internally.
//
// See issue #482 for the full design and motivation: this Provider
// surface replaces ~2,200 LOC of hand-rolled per-type dispatch in the
// downstream reliable repo with a single registry-driven entry point.
package imported

import (
	"encoding/json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/bindings"
)

// Attrs is the JSON payload of a typed Layer-1 resource attribute set.
// Matches the shape stored on pkg/composer/imported.ImportedResource.Attrs;
// expressing it as a type alias rather than re-declaring the underlying
// json.RawMessage keeps a single source of truth and avoids a no-op
// conversion at the producer/consumer boundary.
type Attrs = json.RawMessage

// Capabilities reports the per-type feature surface a Provider
// supports for a given Terraform resource type. The five flags are
// independent so a consumer can degrade individual UI affordances
// (e.g. hide the metrics tab) when a type is partially supported
// without having to special-case the whole type.
//
//   - Discoverable: the cloud-side discoverer can enumerate
//     resources of this type from an account/project scope.
//   - Enrichable: a per-type AttributeEnricher populates the typed
//     Layer-1 ir.Attrs payload for this type.
//   - DriftDetectable: a per-type drift comparator exists for this
//     type (the comparator package owns this; Capabilities just
//     reports whether one is registered).
//   - MetricsAvailable: a ComponentMetricsBinding is registered for
//     this type in pkg/imported/bindings.
//   - AgentEditable: at least one field policy for this type is
//     editable through an interactive-agent write path
//     (EditChatSafe or EditRequiresApproval — the two EditPolicy
//     values an agent can author through normal chat / proposal
//     flows). Agent-name-agnostic: the field reflects the policy
//     surface, not which agent product is wired up downstream.
//     The historical agent-product naming on policy.VisibilityPolicy
//     (VisibilityRileyVisible = "RileyVisible") was rotated to
//     VisibilitySummaryVisible = "SummaryVisible" in #489 so the wire
//     surface is agent-name-agnostic too.
type Capabilities struct {
	Discoverable     bool
	Enrichable       bool
	DriftDetectable  bool
	MetricsAvailable bool
	AgentEditable    bool
}

// ComponentMetricsBinding is the per-type CloudWatch / Cloud
// Monitoring binding the metrics tab dispatches against. Re-exported
// as a type alias from pkg/imported/bindings so callers depending on
// pkg/imported see one canonical name without an extra import.
type ComponentMetricsBinding = bindings.ComponentMetricsBinding

// FieldMismatch describes a single drifted attribute returned by
// Provider.CompareDrift. Snapshot is the value carried in the sealed
// snapshot (what Terraform expects); Cloud is the value the live
// cloud API reported. Field is the dot-and-bracket attribute path
// (see pkg/composer/imported/policy.path grammar).
//
// The Snapshot / Cloud fields are typed `any` because the comparator
// returns whatever shape the typed Attrs payload decoded into —
// scalar values (string, bool, float64), maps, and slices all
// surface here. Downstream consumers JSON-marshal the mismatch list
// for SSE delivery to the UI; the typed `any` round-trips through
// json.Marshal cleanly.
type FieldMismatch struct {
	Field    string
	Snapshot any
	Cloud    any
}

// Clients is a tagged union carrying cloud-specific SDK client
// bundles. Per-cloud Provider impls type-assert the appropriate
// field to their concrete EnrichClients-shaped struct (defined under
// pkg/imported/aws and pkg/imported/gcp). The fields are typed `any`
// rather than typed pointers to keep this package import-free of
// cloud-specific dependencies — see the package doc.
//
// Exactly one of AWS / GCP should be populated per call; passing
// both is undefined behavior. A nil field for the cloud being
// dispatched to is surfaced by the per-cloud Provider as
// ErrEnrichClientUnavailable (or its discover-time equivalent).
type Clients struct {
	AWS any
	GCP any
}

// DiscoverOpts bundles the inputs the Provider.Discover call needs
// beyond the live SDK clients. Each field is optional; per-cloud
// Provider impls map these onto their existing DiscoverArgs shape.
//
//   - Project: stack project name used for server-side
//     labels.project / tag filtering. Empty disables the filter.
//   - Regions: AWS regions or GCP locations to scope the scan.
//     Empty falls back to the per-cloud default (configured region
//     for AWS; no location clause for GCP).
//   - TagSelectors: operator-supplied AND-conjunction of
//     key=value tag/label clauses. Empty disables tag filtering.
//   - AccountID: AWS account ID (resolved out-of-band via STS).
//     Ignored on GCP.
//   - ProjectID: GCP project ID (the real project ID, not the
//     stack project name). Ignored on AWS.
//   - Progress: optional per-Terraform-type completion sink (#699).
//     Nil = no progress (today's behavior, byte-for-byte). When set,
//     it fires once per Terraform type as that type's resources finish
//     being discovered, carrying a running N-of-total type counter so
//     a streaming consumer (reliable's reverse-import "Scan" step) can
//     render real progress instead of a cosmetic timer. Invocations are
//     serialized — safe to call from the AWS path's parallel walk — and
//     CompletedTypes is monotonic 1..TotalTypes.
type DiscoverOpts struct {
	Project      string
	Regions      []string
	TagSelectors []TagSelector
	AccountID    string
	ProjectID    string
	Progress     func(DiscoverProgress)
}

// DiscoverProgress is one per-Terraform-type completion event delivered
// to DiscoverOpts.Progress (discover phase) or EnrichOpts.Progress
// (enrich phase). It is emitted once per type as that type's resources
// finish being discovered or enriched — the incremental signal the
// reverse-import UI needs to stream a per-service / per-type count
// instead of painting a local-timer animation (#699).
//
// Back-compat: a nil sink means no events are produced and the discover
// / enrich return values are unchanged. Existing callers that never set
// a sink are unaffected.
type DiscoverProgress struct {
	// Phase is "discover" (Provider.Discover) or "enrich"
	// (Provider.EnrichAttributes). A single sink can serve both and
	// branch on this field.
	Phase string
	// Type is the Terraform type that just completed, e.g.
	// "aws_s3_bucket".
	Type string
	// FoundCount is the number of resources discovered (discover) or
	// covered (enrich) for this type. Zero is valid.
	FoundCount int
	// CompletedTypes is the running count of types completed so far,
	// inclusive of this one — monotonic 1..TotalTypes across a single
	// Discover / EnrichAttributes call.
	CompletedTypes int
	// TotalTypes is the number of types in this call's scope: the
	// requested type count for discover, the count of distinct
	// enrichable types for enrich. Stable across every event of the
	// call so the consumer's denominator never moves.
	TotalTypes int
}

// EnrichOpts carries optional inputs to Provider.EnrichAttributes. It is
// passed variadically so existing three-argument callers
// (EnrichAttributes(ctx, irs, clients)) keep compiling and behaving
// exactly as before — the zero-opts path is byte-for-byte unchanged.
//
//   - Progress: optional per-Terraform-type completion sink, the enrich
//     counterpart to DiscoverOpts.Progress. Events carry Phase="enrich".
//     Nil = no progress.
//   - Concurrency: bounds the per-resource enrich fan-out. 0 means use
//     the package default.
type EnrichOpts struct {
	Progress func(DiscoverProgress)
	// Concurrency bounds the per-resource enrich fan-out. 0 means use
	// the package default.
	Concurrency int
}

// TagSelector is a single operator-supplied tag/label equality
// clause. Mirrors awsdiscover.TagSelector / gcpdiscover.TagSelector
// — declared here at the cloud-agnostic boundary so callers can
// construct DiscoverOpts without importing a cloud-specific package.
type TagSelector struct {
	Key   string
	Value string
}
