package imported

import (
	"context"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// Provider is the cloud-agnostic facade every consumer of imported
// resources dispatches through. One implementation per cloud
// (pkg/imported/aws, pkg/imported/gcp); construct via ProviderFor or
// the per-cloud package's NewProvider.
//
// The interface bundles every per-Terraform-type concern a consumer
// needs — static introspection (SupportedTypes, Capabilities, labels,
// policy, metrics), identity-shape helpers, live cloud interaction
// (Discover/Enrich), and the compare + render helpers. See issue #482
// for the full design rationale: this surface replaces ~2,200 LOC of
// hand-rolled per-type dispatch in the downstream consumer.
//
// Methods are grouped by category in the interface declaration; all
// methods are safe to call concurrently per the per-cloud impls.
type Provider interface {
	// SupportedTypes returns the sorted, deterministic list of
	// Terraform resource types this Provider knows about. Matches
	// pkg/insideout-import/registry.SupportedDiscoverTypes(cloud).
	SupportedTypes() []string

	// Capabilities reports the per-type feature surface for tfType.
	// Returns a zero-valued Capabilities (all false) for unknown
	// types — consumers branch on the bools rather than checking
	// the type against SupportedTypes() separately.
	Capabilities(tfType string) Capabilities

	// LabelFor returns the human-readable display label and the
	// icon-asset key for tfType. Falls back to the default rule
	// (humanized cloud-prefix-stripped type name) when no curated
	// override is registered. See pkg/imported/labels.
	LabelFor(tfType string) (label, iconKey string)

	// PolicyFor returns the curated FieldPolicy map for tfType, or
	// (nil, false) when no policy is registered. The map is keyed
	// by attribute path (see pkg/composer/imported/policy.path
	// grammar); a nil map signals an Identity-only type whose
	// attribute surface is not curated.
	PolicyFor(tfType string) (policy.Map, bool)

	// MetricsBinding returns the ComponentMetricsBinding for
	// tfType, or (zero, false) when no binding is registered. See
	// pkg/imported/bindings. Consumers gate the metrics tab on the
	// boolean — a zero binding with ok==true is valid and means
	// "use consumer defaults", distinct from "no metrics surface".
	MetricsBinding(tfType string) (ComponentMetricsBinding, bool)

	// StableID returns a deterministic, collision-resistant
	// identifier for the resource named by identity. Used for
	// dedupe keys, audit log entries, and stable URL paths in the
	// downstream UI. Distinct from Address (which is the
	// Terraform-state-form key) — StableID is opaque and may
	// outlive an Address rename.
	StableID(identity *composerimported.ResourceIdentity) string

	// CanonicalAddress returns the Terraform resource address
	// (`<type>.<label>`) for identity, regenerating it from the
	// underlying NameHint / NativeIDs when identity.Address is
	// empty. Equivalent to imported.GenerateAddress for a fresh
	// identity; round-trips identity.Address when set.
	CanonicalAddress(identity *composerimported.ResourceIdentity) string

	// Discover enumerates resources of the named types from the
	// caller's cloud account/project. The clients union must
	// carry the appropriate cloud-side SDK client bundle; the
	// opts struct supplies filters and scope. Returns one
	// ImportedResource per matched cloud resource, with
	// Identity populated and Tier=TierImportedFlat,
	// Source=SourceImporter.
	Discover(ctx context.Context, types []string, clients Clients, opts DiscoverOpts) ([]composerimported.ImportedResource, error)

	// EnrichAttributes populates ir.Attrs in place for every
	// resource whose Identity.Type has a registered enricher.
	// Resources of types without an enricher are left untouched.
	// Errors are accumulated per-resource and returned as a
	// joined error; ErrEnrichClientUnavailable failures are
	// downgraded internally (the per-cloud impls surface them as
	// progress warnings without failing the batch).
	EnrichAttributes(ctx context.Context, irs []composerimported.ImportedResource, clients Clients) error

	// EnrichByID fetches the typed Layer-1 Attrs payload for a
	// single resource named by identity. Used by the per-IR drift
	// refresh path. Returns ErrEnrichByIDNotImplemented when the
	// per-type enricher does not satisfy the ByIDEnricher
	// contract; callers downgrade that to "skip drift refresh for
	// this type" rather than treating it as a hard error.
	EnrichByID(ctx context.Context, identity *composerimported.ResourceIdentity, clients Clients) (Attrs, error)

	// CompareDrift diffs snapshot against live attrs for tfType
	// and returns the list of mismatched fields. Returns nil
	// when no drift is detected and when no comparator is
	// registered for tfType (consumers check
	// Capabilities(tfType).DriftDetectable to distinguish the two
	// cases). Field-level semantics (whole-list compare, label-
	// filter, etc.) come from the per-type policy's
	// DriftSemantic tags.
	CompareDrift(tfType string, snapshot, live Attrs) []FieldMismatch

	// AgentContext returns the per-Terraform-type policy block +
	// per-instance value rows for inclusion in an interactive-agent
	// chat-context prompt. Per registered type, in stable type-name
	// order, the renderer emits:
	//
	//   == Imported.<type> ==
	//   editable_chat_safe: [<paths>]
	//   editable_with_approval: [<paths>]
	//   read_only: [<paths>]
	//   system_owned: [<paths>]
	//   # sensitive fields omitted entirely
	//   instances:
	//     <address>:
	//       project: <project>           // GCP identity slot
	//       location: <location>         // identity slot
	//       <path>: <current-value>      // per VisibleFieldsFor
	//   == End ==
	//
	// Instances within each type sort by Identity.Address. Types
	// without a registered policy in pkg/composer/imported/policy
	// are skipped entirely — emitting a half-rendered block with
	// no policy summary would teach the agent nothing while burning
	// context tokens.
	//
	// Both per-cloud Provider impls delegate to the shared
	// RenderAgentContext helper because the rendering is
	// cloud-agnostic.
	//
	// Agent-name-agnostic on purpose: the per-product naming can rotate
	// without flipping the interface.
	AgentContext(irs []composerimported.ImportedResource) []string
}
