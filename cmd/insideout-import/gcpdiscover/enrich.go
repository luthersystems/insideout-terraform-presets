package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/cloudkms/v1"
	computev1 "google.golang.org/api/compute/v1"
	iamv1 "google.golang.org/api/iam/v1"
	identitytoolkitv2 "google.golang.org/api/identitytoolkit/v2"
	loggingv2 "google.golang.org/api/logging/v2"
	monitoringv1 "google.golang.org/api/monitoring/v1"
	monitoringv3 "google.golang.org/api/monitoring/v3"
	secretmanagerv1 "google.golang.org/api/secretmanager/v1"
	servicenetworkingv1 "google.golang.org/api/servicenetworking/v1"
	serviceusagev1 "google.golang.org/api/serviceusage/v1"
	sqladminv1 "google.golang.org/api/sqladmin/v1"
	storagev1 "google.golang.org/api/storage/v1"
	vpcaccessv1 "google.golang.org/api/vpcaccess/v1"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// defaultEnrichConcurrency bounds the per-resource cloud-API fan-out of
// EnrichAttributes. Each enricher issues one or more GCP SDK round-trips
// per resource; running them under a bounded worker pool turns the
// previously serial ~1m30s pass on large accounts (~224 resources) into
// a parallel one without unbounded fan-out hammering the GCP API rate
// limits.
//
// Pinned to 4 to mirror awsdiscover.defaultDiscoverTypesConcurrency,
// which was lowered from 8 to 4 in #632 after 8 simultaneous t=0
// kickoffs tripped CloudControl's per-account rate budget with a
// ThrottlingException. GCP's per-project quotas are likewise burst-
// sensitive, so the enrich phase inherits the same ceiling. 4 still
// delivers roughly a 4x wall-time saving over the old serial pass —
// the marginal speedup from 8 isn't worth re-introducing the throttle
// risk.
const defaultEnrichConcurrency = 4

// defaultEnrichStartupJitterMax bounds the random sleep applied before
// the first defaultEnrichConcurrency enrich goroutines issue their
// first GCP call. Mirrors awsdiscover.defaultDiscoverStartupJitterMax
// (same value, same rationale): without jitter the first batch of
// goroutines all kick off at t=0 and their opening Get/List calls land
// in the same burst, lighting up the per-project rate limiter. 500ms
// spreads the load without materially extending wall time. Only the
// initial batch is jittered — goroutines beyond the first
// defaultEnrichConcurrency are already naturally staggered by errgroup
// slot availability.
const defaultEnrichStartupJitterMax = 500 * time.Millisecond

// enrichRetryMaxAttempts caps how many times enrichWithRetry calls the
// wrapped enricher when it keeps returning a rate-limit error. Mirrors
// awsdiscover.enrichRetryMaxAttempts / discoverRetryMaxAttempts.
const enrichRetryMaxAttempts = 8

// enrichRetryBaseDelay and enrichRetryMaxDelay bound the exponential
// backoff enrichWithRetry sleeps between throttled attempts: the delay
// starts at enrichRetryBaseDelay and doubles each attempt, capped at
// enrichRetryMaxDelay.
//
// These are declared as package-level vars (not consts) purely so the
// test suite can shrink them via a defer-restore to keep retry tests
// fast; production code never reassigns them.
var (
	enrichRetryBaseDelay = 200 * time.Millisecond
	enrichRetryMaxDelay  = 8 * time.Second
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
	// IAMPolicyLister is the shared per-service GetIamPolicy seam reused
	// by every IAM-binding enricher (project_iam_member, storage_bucket_
	// iam_member, kms_crypto_key_iam_binding, secret_manager_secret_iam_
	// member, secret_manager_secret_iam_binding, cloud_run_v2_service_iam_
	// member, cloudfunctions2_function_iam_member). One unified interface
	// fronts six per-service SDK clients so the EnrichClients surface
	// doesn't grow per added IAM resource type. Nil tolerated per the
	// same convention: IAM enrichers report ErrEnrichClientUnavailable.
	IAMPolicyLister gcpIAMPolicyLister
	// Final-push enrichers (#482) — three non-CAI enrichers stacked on
	// Bundle G6 to push GCP enrichable coverage past 95%. Each is a
	// single per-service SDK client; nil tolerated per the same
	// convention as the others.
	//
	// ServiceUsage backs google_project_service (Services.Get on the
	// `projects/<p>/services/<svc>` resource name).
	// ServiceNetworking backs google_service_networking_connection
	// (Services.Connections.List filtered by network — no per-connection
	// Get exists on the API).
	// VPCAccess backs google_vpc_access_connector
	// (Projects.Locations.Connectors.Get on the full connector path).
	ServiceUsage      *serviceusagev1.Service
	ServiceNetworking *servicenetworkingv1.APIService
	VPCAccess         *vpcaccessv1.Service
	ProjectID         string
}

// ErrEnrichClientUnavailable signals that the SDK client an enricher
// needs is nil on EnrichClients. Distinguishable from a real API
// error so EnrichAttributes can downgrade it to a per-resource warning
// (the type is silently un-enriched in the output) instead of a batch-
// fatal error. Mirrors the existing nonCAIDiscovererHasLister warn
// path in DiscoverTypes (gcpdiscover.go:430).
var ErrEnrichClientUnavailable = errors.New("enrich: required SDK client unavailable on EnrichClients")

// enrichWithRetry calls fn and, if it returns a rate-limit error (per
// isGoogleAPIRateLimited), retries it under an exponential backoff with
// jitter — up to enrichRetryMaxAttempts total attempts. A nil result, a
// non-throttle error, or exhausting the attempt budget returns
// immediately. The backoff sleep is select-cancellable on ctx.Done();
// on cancel the last error is returned.
//
// This is a BACKSTOP retry loop. The AWS sibling layers this ON TOP of
// the AWS SDK adaptive retryer; GCP has no equivalent client-level
// adaptive retryer, so on the GCP side this loop is the PRIMARY throttle
// defense — every rate-limit hit during enrichment is absorbed here
// rather than surfacing as a per-resource error.
//
// Re-running a soft-failed Enrich is safe: per-type enrichers marshal
// ir.Attrs atomically, so a retried call simply overwrites the prior
// (failed) attempt's partial state.
func enrichWithRetry(ctx context.Context, fn func() error) error {
	var err error
	backoff := enrichRetryBaseDelay
	for attempt := 1; ; attempt++ {
		err = fn()
		if err == nil || !isGoogleAPIRateLimited(err) || attempt >= enrichRetryMaxAttempts {
			return err
		}
		// Half-fixed + half-random of the current backoff window so
		// retrying goroutines don't re-synchronize into a fresh burst.
		sleep := backoff/2 + time.Duration(rand.Int63n(int64(backoff/2)+1))
		t := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		if backoff < enrichRetryMaxDelay {
			backoff *= 2
			if backoff > enrichRetryMaxDelay {
				backoff = enrichRetryMaxDelay
			}
		}
	}
}

// EnrichAttributes populates ir.Attrs in place for every imported
// resource whose Identity.Type has a registered enricher. Resources
// of types without a registered enricher are left untouched; the
// caller can detect this via len(ir.Attrs) == 0 on return.
//
// The per-resource enrichers run concurrently under a bounded
// errgroup (defaultEnrichConcurrency workers) so a large account's
// cloud-API round-trips overlap instead of running strictly serially.
// Each goroutine writes a distinct irs[i] element, so the fan-out is
// data-race-free; the group is used purely as a bounded worker pool and
// never fails early. Progress emission, EnrichmentStatus/EnrichErrors
// stamping, and error aggregation happen in a SECOND serial pass after
// the workers finish, walking idx in sorted order — so emit order and
// error-join order remain exactly as deterministic as the old serial
// implementation, regardless of which worker completes first.
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

	// Fan the per-resource enrichers out across a bounded worker pool.
	// results[pos] holds the error (or nil) for idx[pos]; each worker
	// writes exactly one distinct results slot and one distinct irs
	// element, so no synchronization beyond errgroup is needed. The
	// closure always returns nil — the group is a bounded pool, never
	// an early-cancel mechanism — because the documented contract is
	// "a partial result is more useful than no result": every resource
	// must be attempted regardless of sibling failures. ctx is the
	// caller's context (not a derived errgroup context) since we never
	// cancel early.
	results := make([]error, len(idx))
	var grp errgroup.Group
	grp.SetLimit(defaultEnrichConcurrency)
	for pos, i := range idx {
		enr := g.byTypeEnricher[irs[i].Identity.Type]
		grp.Go(func() error {
			// Jitter only the initial burst: the first
			// defaultEnrichConcurrency goroutines start simultaneously
			// at t=0, so their opening GCP calls would otherwise land
			// together. Goroutines beyond that batch are already
			// staggered by errgroup slot availability — jittering all
			// of them would add tens of seconds of dead wall time.
			if pos < defaultEnrichConcurrency {
				sleep := time.Duration(rand.Int63n(int64(defaultEnrichStartupJitterMax)))
				t := time.NewTimer(sleep)
				select {
				case <-ctx.Done():
					t.Stop()
				case <-t.C:
				}
			}
			results[pos] = enrichWithRetry(ctx, func() error {
				return enr.Enrich(ctx, &irs[i], clients)
			})
			return nil
		})
	}
	_ = grp.Wait()

	// Per-type progress (#699): mirror the discover phase — when the
	// emitter additionally implements TypeProgressEmitter (the
	// pkg/imported facade's bridge does; the wire JSONEmitter /
	// NopEmitter do not), fire one TypeDone per enriched type. idx is
	// sorted by (type, address), so a type's resources are contiguous;
	// emit when the type changes and once more after the loop for the
	// final type. totalEnrichTypes is the count of distinct enrichable
	// types — the stable N-of-total denominator. Found counts the
	// resources of the type this pass covered (matching the discover
	// phase's "resources found"), independent of per-resource enrich
	// success so the value is deterministic.
	typeSink, _ := emitter.(progress.TypeProgressEmitter)
	totalEnrichTypes := 0
	for pos := range idx {
		if pos == 0 || irs[idx[pos-1]].Identity.Type != irs[idx[pos]].Identity.Type {
			totalEnrichTypes++
		}
	}
	emitTypeDone := func(tfType string, found int) {
		if typeSink != nil {
			typeSink.TypeDone(progress.TypeProgress{
				Phase:  "enrich",
				TFType: tfType,
				Found:  found,
				Total:  totalEnrichTypes,
			})
		}
	}

	// Second pass: walk idx in sorted order to inspect results, stamp
	// the per-IR EnrichmentStatus/EnrichErrors, emit progress events,
	// and aggregate errors. Doing this serially — and outside the
	// goroutines — keeps emit order and error-join order deterministic
	// (progress.Emitter is not documented as concurrency-safe) and
	// identical to the old serial behaviour.
	var errs []error
	var curType string
	curFound := 0
	for pos, i := range idx {
		if t := irs[i].Identity.Type; pos > 0 && t != curType {
			emitTypeDone(curType, curFound)
			curFound = 0
		}
		curType = irs[i].Identity.Type
		curFound++
		err := results[pos]
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
	if len(idx) > 0 {
		emitTypeDone(curType, curFound)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
