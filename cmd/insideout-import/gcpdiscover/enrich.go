package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"google.golang.org/api/cloudkms/v1"
	computev1 "google.golang.org/api/compute/v1"
	iamv1 "google.golang.org/api/iam/v1"
	identitytoolkitv2 "google.golang.org/api/identitytoolkit/v2"
	loggingv2 "google.golang.org/api/logging/v2"
	monitoringv1 "google.golang.org/api/monitoring/v1"
	monitoringv3 "google.golang.org/api/monitoring/v3"
	pubsubv1 "google.golang.org/api/pubsub/v1"
	secretmanagerv1 "google.golang.org/api/secretmanager/v1"
	sqladminv1 "google.golang.org/api/sqladmin/v1"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// enrichServiceSlug is the progress-event service slug for the SDK
// attribute-enrichment phase. Distinct from the CAI / non-CAI slugs so
// progress consumers can attribute events correctly when EnrichAttributes
// runs interleaved with or after DiscoverTypes.
const enrichServiceSlug = "gcp_sdk_enrich"

// AttributeEnricher is the per-resource-type contract for populating
// `imported.ImportedResource.Attrs` (the typed Layer 1 payload) from a
// cloud-side SDK Get/Describe call. Sibling interface to Discoverer:
// Discoverers produce Identity-only records via Cloud Asset Inventory
// (one CAI fanout per project), and AttributeEnrichers later turn each
// into a fully-populated record by calling the resource type's own
// SDK API and writing the result into ir.Attrs.
//
// Why this exists: Stage 2b's `terraform plan -generate-config-out`
// path needs a `terraform` binary in $PATH and several round-trips per
// resource. UI/SaaS consumers (e.g. a Vercel handler — see
// luthersystems/reliable#1346) can't shell out to terraform under
// their runtime constraints; this is the terraform-binary-free path
// that produces decision-#34-clean HCL via composer.EmitImportedTF.
//
// Why ir.Attrs not ir.Attributes: composer.EmitImportedTF's opaque
// `Attributes` path (pkg/composer/imported_emit.go:236) routes
// `map[string]any` through cty.ObjectVal, which emits an HCL `{ ... }`
// literal — never a sub-block. For resources whose configurable
// surface is dominated by nested blocks (storage_bucket has 12 such
// blocks: versioning, lifecycle_rule, cors, encryption, ...) the
// opaque path can't reach decision #34. The typed `Attrs` path
// (imported_emit.go:218-228) calls generated.MarshalHCL which DOES
// emit nested blocks correctly, so enrichers populate that.
//
// Per-type enrichers map their cloud SDK response struct into the
// matching pkg/composer/imported/generated.Google<Type> typed model
// and JSON-marshal it into ir.Attrs. Per decision #5 (managed-
// resource-tiers.md "Composer emission rule") computed-only fields
// are not populated; per decision #36 sensitive fields follow the
// downstream redaction policy (the enricher writes the value, the
// emit/persist layers redact at write time — Phase-1 storage_bucket
// has no Sensitive fields anyway).
type AttributeEnricher interface {
	// ResourceType returns the Terraform type this enricher covers,
	// e.g. "google_storage_bucket". Must match the registered
	// Discoverer of the same type.
	ResourceType() string

	// Enrich fetches live cloud-side state for ir (whose Identity is
	// already populated by the corresponding Discoverer) and writes
	// the typed payload into ir.Attrs. The enricher must not touch
	// ir.Identity. clients carries the SDK clients the enricher
	// needs; a nil required client is reported as ErrEnrichClientUnavailable
	// so callers can distinguish "not configured" from "real API
	// error".
	Enrich(ctx context.Context, ir *imported.ImportedResource, clients EnrichClients) error
}

// ByIDEnricher is an optional sibling to AttributeEnricher that fetches
// a single resource's typed Layer 1 payload from its ResourceIdentity
// alone — used by the per-IR drift refresh path that the eventual
// pkg/imported.Provider.EnrichByID exposes (presets#482). Implementing
// this interface is purely additive: enrichers that satisfy only
// AttributeEnricher continue to work unchanged for the batch enrichment
// flow; the by-ID dispatcher type-asserts at call time and downgrades
// gracefully when the assertion fails.
//
// Mirrors awsdiscover.ByIDEnricher (same name, same shape, per-cloud
// EnrichClients). The two clouds are deliberately kept symmetric so
// the unified pkg/imported.Provider in presets#482 can dispatch
// through identical interface shapes.
//
// The shape diverges from Enrich for two reasons:
//
//   - The caller starts from an Identity that hasn't been produced by
//     the corresponding Discoverer (e.g. a UI refresh on a single row),
//     so there is no pre-existing *ImportedResource to mutate.
//   - The caller wants only the raw typed payload (returned as the
//     same json.RawMessage shape that lands in ImportedResource.Attrs),
//     so it can drive its own comparator without going through the
//     batch progress / aggregation machinery.
//
// Implementations must not mutate identity. In the common case Enrich
// and EnrichByID share a private helper that does the SDK call and
// emits the typed struct; the two methods differ only in how they
// package the result.
type ByIDEnricher interface {
	// ResourceType returns the Terraform type this enricher covers.
	// Must match the registered Discoverer / AttributeEnricher of the
	// same type.
	ResourceType() string

	// EnrichByID fetches the typed Layer 1 payload for the resource
	// named by identity and returns it as the json.RawMessage that
	// would land in ImportedResource.Attrs. A nil required client on
	// clients is reported as ErrEnrichClientUnavailable so callers
	// can distinguish "not configured" from "real API error". A
	// not-found resource is reported as ErrNotFound (sentinel
	// declared in gcpdiscover.go).
	EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients EnrichClients) (json.RawMessage, error)
}

// EnrichClients bundles the cloud SDK clients per-type enrichers
// dispatch to. Construct once per discover run (the lifecycle is
// owned by the caller — close the underlying clients when done).
// A nil field is tolerated: enrichers whose required client is nil
// return ErrEnrichClientUnavailable, which EnrichAttributes
// surfaces as a per-resource progress.ServiceWarn rather than
// failing the whole batch. ProjectID is the real GCP project ID
// (the same value passed to NewGCPDiscoverer), threaded through
// for enrichers whose cloud SDK doesn't return a project-ID-as-
// string field — google.golang.org/api/storage/v1.Bucket reports
// only ProjectNumber (uint64), so the enricher pulls the string
// project ID from here to populate the TF `project` attribute.
//
// CloudAsset is the optional Cloud Asset Inventory getter that backs
// the CAI HYBRID enricher (cloudasset_enricher.go, mirrors AWS #490 for
// GCP). One unified getter fronts every TF type registered in
// cloudAssetTypeConfigs whose Layer-1 codegen has shipped — far fewer
// per-service SDK clients than the hand-rolled enrichment path needed
// pre-HYBRID. Nil tolerated: types whose hand-rolled enricher wins as
// an override don't need this client; types without a hand-rolled
// override return ErrEnrichClientUnavailable when this is nil, which
// the EnrichAttributes loop downgrades to a per-resource warning.
type EnrichClients struct {
	Storage       *storagev1.Service
	Pubsub        *pubsubv1.Service
	SecretManager *secretmanagerv1.Service
	Compute       *computev1.Service
	// Bundle G5 (#482) — added for KMS, SQL, and IAM enrichers.
	// KMS backs google_kms_crypto_key; SQLAdmin backs
	// google_sql_database_instance; IAM backs google_service_account.
	// Nil tolerated per the same convention as the other clients:
	// affected enrichers report ErrEnrichClientUnavailable.
	KMS      *cloudkms.Service
	SQLAdmin *sqladminv1.Service
	IAM      *iamv1.Service
	// Bundle G6 (#482) — added for Logging, IdentityToolkit, and
	// Monitoring enrichers. Logging backs google_logging_project_sink;
	// IdentityToolkit backs google_identity_platform_config;
	// Monitoring (v3) backs alert policies and notification channels;
	// MonitoringV1 backs dashboards (v1 schema). Nil tolerated per the
	// same convention as the other clients.
	Logging         *loggingv2.Service
	IdentityToolkit *identitytoolkitv2.Service
	Monitoring      *monitoringv3.Service
	// MonitoringV1 is the Cloud Monitoring v1 SDK service. Used for
	// dashboards (v1 schema); v3 (the Monitoring field above) covers
	// AlertPolicies, NotificationChannels, etc. — the two SDK packages
	// expose disjoint resource families and the dashboards-only client
	// is kept as a separate field so the wiring is explicit.
	MonitoringV1 *monitoringv1.Service
	CloudAsset   gcpAssetGetter
	ProjectID    string
}

// ErrEnrichClientUnavailable signals that the SDK client an enricher
// needs is nil on EnrichClients. Distinguishable from a real API
// error so EnrichAttributes can downgrade it to a per-resource warning
// (the type is silently un-enriched in the output) instead of a batch-
// fatal error. Mirrors the existing nonCAIDiscovererHasLister warn
// path in DiscoverTypes (gcpdiscover.go:430).
var ErrEnrichClientUnavailable = errors.New("enrich: required SDK client unavailable on EnrichClients")

// EnrichAttributes populates ir.Attrs in place for every imported
// resource whose Identity.Type has a registered enricher. Resources
// of types without a registered enricher are left untouched; the
// caller can detect this via len(ir.Attrs) == 0 on return.
//
// Errors are accumulated per-resource and surfaced together at the
// end so a single mid-batch failure doesn't lose results from earlier
// successful enrichments — a partial result is more useful than no
// result. ErrEnrichClientUnavailable failures are downgraded to
// progress.ServiceWarn events (and not included in the returned
// error) since they reflect caller-side configuration, not API
// failures. The returned error wraps the joined per-resource errors;
// callers may inspect via errors.Is / errors.As.
//
// emitter receives ItemFound per successfully enriched resource and
// ServiceWarn per ErrEnrichClientUnavailable. The standard
// (ServiceStart, ServiceFinish) pair brackets the whole batch under
// enrichServiceSlug.
func (g *GCPDiscoverer) EnrichAttributes(ctx context.Context, irs []imported.ImportedResource, clients EnrichClients, emitter progress.Emitter) error {
	if emitter == nil {
		emitter = progress.NopEmitter{}
	}
	stageStart := time.Now()
	emitter.ServiceStart(enrichServiceSlug, "")
	defer func() { emitter.ServiceFinish(enrichServiceSlug, "", len(irs), time.Since(stageStart)) }()

	// Dispatch in deterministic order so progress events and any
	// emitted warnings are stable across runs.
	idx := make([]int, 0, len(irs))
	for i := range irs {
		if _, ok := g.byTypeEnricher[irs[i].Identity.Type]; ok {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ia, ib := idx[a], idx[b]
		if irs[ia].Identity.Type != irs[ib].Identity.Type {
			return irs[ia].Identity.Type < irs[ib].Identity.Type
		}
		return irs[ia].Identity.Address < irs[ib].Identity.Address
	})

	var errs []error
	for _, i := range idx {
		enr := g.byTypeEnricher[irs[i].Identity.Type]
		err := enr.Enrich(ctx, &irs[i], clients)
		switch {
		case err == nil:
			// Per-type enrichers marshal Attrs atomically today, so
			// a nil return means Full. The Partial state is reserved
			// for a future multi-call enricher (see issue #471).
			irs[i].Identity.EnrichmentStatus = imported.EnrichmentStatusFull
			irs[i].Identity.EnrichErrors = nil
			emitter.ItemFound(enrichServiceSlug, irs[i].Identity.Location, irs[i].Identity.Type, irs[i].Identity.ImportID)
		case errors.Is(err, ErrEnrichClientUnavailable):
			// Client-side configuration failure — surface as a
			// warn but don't accumulate as an error. Mirrors the
			// nonCAIDiscovererHasLister warn semantics. The typed
			// signal on Identity lets downstream consumers
			// distinguish this from a happy Identity-only IR
			// (issue #471).
			irs[i].Identity.EnrichmentStatus = imported.EnrichmentStatusFailed
			irs[i].Identity.EnrichErrors = append(irs[i].Identity.EnrichErrors, err.Error())
			emitter.ServiceWarn(enrichServiceSlug, "", fmt.Sprintf("%s/%s: %v", irs[i].Identity.Type, irs[i].Identity.Address, err))
		default:
			wrapped := fmt.Errorf("enrich %s/%s: %w", irs[i].Identity.Type, irs[i].Identity.Address, err)
			irs[i].Identity.EnrichmentStatus = imported.EnrichmentStatusFailed
			irs[i].Identity.EnrichErrors = append(irs[i].Identity.EnrichErrors, wrapped.Error())
			errs = append(errs, wrapped)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
