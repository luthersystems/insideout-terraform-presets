// Package awsdiscover — AWS-side attribute enrichment (#457).
//
// Mirrors the GCP-side AttributeEnricher / EnrichClients / EnrichAttributes
// surface in cmd/insideout-import/gcpdiscover/enrich.go so the two clouds
// expose a symmetric contract to the discover orchestrator. See that file's
// doc-comment for the rationale (Stage 2b terraform-binary-free path,
// ir.Attrs vs ir.Attributes, decision-#34 clean HCL, ErrEnrichClientUnavailable
// downgrade semantics).
//
// Bundle scope: aws_dynamodb_table lands first because its Layer 1 typed
// struct already exists in pkg/composer/imported/generated. aws_s3_bucket
// follows once presets bundle #461 adds it to WantedAWS — the same
// dispatcher + EnrichClients infrastructure will pick it up with a one-line
// byTypeEnricher registration.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// defaultEnrichConcurrency bounds the per-resource cloud-API fan-out of
// EnrichAttributes. Each enricher issues one or more AWS SDK round-trips
// per resource; running them under a bounded worker pool turns the
// previously serial ~1m30s pass on large accounts (~224 resources) into
// a parallel one without unbounded fan-out hammering the AWS API rate
// limits.
//
// Pinned to 4 to mirror awsdiscover.defaultDiscoverTypesConcurrency,
// which was lowered from 8 to 4 in #632 after 8 simultaneous t=0
// kickoffs tripped CloudControl's per-account rate budget with a
// ThrottlingException. The enrich phase issues the same flavor of
// per-account AWS calls, so it inherits the same ceiling. 4 still
// delivers roughly a 4x wall-time saving over the old serial pass —
// the marginal speedup from 8 isn't worth re-introducing the throttle
// risk.
const defaultEnrichConcurrency = 4

// defaultEnrichStartupJitterMax bounds the random sleep applied before
// the first defaultEnrichConcurrency enrich goroutines issue their
// first AWS call. Mirrors awsdiscover.defaultDiscoverStartupJitterMax
// (same value, same rationale): without jitter the first batch of
// goroutines all kick off at t=0 and their opening Describe*/Get* calls
// land in the same burst, lighting up the per-account rate limiter.
// 500ms gives the SDK retryer's adaptive token bucket room to spread
// the load without materially extending wall time. Only the initial
// batch is jittered — goroutines beyond the first defaultEnrichConcurrency
// are already naturally staggered by errgroup slot availability.
const defaultEnrichStartupJitterMax = 500 * time.Millisecond

// enrichRetryMaxAttempts caps how many times enrichWithRetry calls the
// wrapped enricher when it keeps returning a throttle error. Mirrors
// discoverRetryMaxAttempts (cmd/insideout-import/discover.go).
const enrichRetryMaxAttempts = 8

// enrichRetryBaseDelay and enrichRetryMaxDelay bound the exponential
// backoff enrichWithRetry sleeps between throttled attempts: the delay
// starts at enrichRetryBaseDelay and doubles each attempt, capped at
// enrichRetryMaxDelay.
//
// These are declared as package-level vars (not consts) purely so the
// test suite can shrink them via a defer-restore to keep retry tests
// fast; production code never reassigns them. The retry path almost
// never fires in production (see enrichWithRetry's doc comment), so the
// observable behavior of the var-vs-const choice is test-only.
var (
	enrichRetryBaseDelay = 200 * time.Millisecond
	enrichRetryMaxDelay  = 8 * time.Second
)

// enrichServiceSlug is the progress-event service slug for the SDK
// attribute-enrichment phase. Distinct from per-discoverer slugs so
// progress consumers can attribute events correctly when EnrichAttributes
// runs interleaved with or after DiscoverTypes. Mirrors the GCP-side
// gcpdiscover.enrichServiceSlug ("gcp_sdk_enrich").
const enrichServiceSlug = "aws_sdk_enrich"

// AttributeEnricher is the per-resource-type contract for populating
// `imported.ImportedResource.Attrs` (the typed Layer 1 payload) from a
// cloud-side SDK Describe / Get call. Sibling to Discoverer: Discoverers
// produce Identity-only records via Cloud Control (or the hand-rolled
// per-service path), and AttributeEnrichers later turn each into a fully-
// populated record by issuing per-type AWS SDK calls and writing the
// typed result into ir.Attrs.
//
// Why this exists: Stage 2b's `terraform plan -generate-config-out`
// path needs a `terraform` binary in $PATH and several round-trips per
// resource. UI/SaaS consumers (e.g. luthersystems/reliable's Vercel
// handler in #1346) can't shell out to terraform under their runtime
// constraints; this is the terraform-binary-free path that produces
// decision-#34-clean HCL via composer.EmitImportedTF. Mirrors the
// gcpdiscover.AttributeEnricher contract (presets#403); this is the
// AWS-side parity (presets#457).
//
// Per-type enrichers map their cloud SDK response struct into the
// matching pkg/composer/imported/generated.AWS<Type> typed model and
// JSON-marshal it into ir.Attrs. Per decision #5 computed-only fields
// are not populated; per decision #36 sensitive fields follow the
// downstream redaction policy.
type AttributeEnricher interface {
	// ResourceType returns the Terraform type this enricher covers,
	// e.g. "aws_dynamodb_table". Must match the registered Discoverer
	// of the same type.
	ResourceType() string

	// Enrich fetches live cloud-side state for ir (whose Identity is
	// already populated by the corresponding Discoverer) and writes
	// the typed payload into ir.Attrs. The enricher must not touch
	// ir.Identity. clients carries the SDK clients the enricher
	// needs; a nil required client is reported as
	// ErrEnrichClientUnavailable so callers can distinguish "not
	// configured" from "real API error".
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
// Implementations must not mutate identity. They may issue the same
// SDK calls as the AttributeEnricher implementation for the same type;
// in the common case Enrich and EnrichByID share a private helper that
// does the SDK call and emits the typed struct, and the two methods
// differ only in how they package the result.
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
	// not-found resource is reported as ErrNotFound.
	EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients EnrichClients) (json.RawMessage, error)
}

// EnrichClients bundles the AWS SDK clients per-type enrichers dispatch
// to. Construct once per discover run (the lifecycle is owned by the
// caller — the AWS SDK clients are stateless wrappers over an aws.Config,
// so callers can construct/discard freely).
//
// A nil field is tolerated: enrichers whose required client is nil
// return ErrEnrichClientUnavailable, which EnrichAttributes surfaces as
// a per-resource progress.ServiceWarn rather than failing the whole
// batch.
//
// AccountID parallels gcpdiscover.EnrichClients.ProjectID — it's the
// AWS account ID resolved out-of-band (typically via STS
// GetCallerIdentity at the start of the discover run) and threaded
// through here for enrichers that need to construct ARNs or stamp
// account-scoped fields without an extra STS round-trip per resource.
// Empty is tolerated when no enricher uses it (today: DynamoDB doesn't
// need it because TableArn comes back in DescribeTable directly).
type EnrichClients struct {
	S3       *s3.Client
	DynamoDB *dynamodb.Client
	// CloudWatchLogs removed in #502 — the aws_cloudwatch_log_group
	// hand-rolled enricher retired in favor of the generic Cloud Control
	// + Normalizer path. The Cloud Control discoverer's listing fallback
	// constructs its own cloudwatchlogs.Client inline (see
	// cloudcontrol_listers.go), so this field had no remaining consumer.
	SecretsManager *secretsmanager.Client
	// Bedrock is the shared client used by the bedrock_guardrail and
	// bedrock_model_invocation_logging_configuration enrichers (#482
	// Bucket-C push). The SDK client is stateless over an aws.Config so
	// a single instance serves every Bedrock-routed enricher. Nil is
	// tolerated and surfaces as ErrEnrichClientUnavailable at Enrich
	// time; EnrichAttributes downgrades that to a per-resource ServiceWarn.
	Bedrock *bedrock.Client
	// ServiceDiscovery is the shared client used by the
	// service_discovery_private_dns_namespace enricher (#482 Bucket-C
	// push). Same nil-tolerated discipline as Bedrock.
	ServiceDiscovery *servicediscovery.Client
	// CloudControl is the shared client for the generic Cloud Control
	// enricher (#490 HYBRID). One client is reused across every
	// cloudControlEnricher registered in NewAWSDiscoverer.byTypeEnricher
	// — the SDK client is stateless over an aws.Config so a single
	// instance serves every CFN type. Nil is tolerated and surfaces as
	// ErrEnrichClientUnavailable at Enrich time; EnrichAttributes
	// downgrades that to a per-resource ServiceWarn.
	CloudControl *cloudcontrol.Client
	// ResourceExplorer2 is the shared client for the Resource Explorer 2
	// hand-rolled enrichers (aws_resourceexplorer2_index,
	// aws_resourceexplorer2_view). The SDK client carries an aws.Config
	// across regions; each enricher applies its per-call region override
	// via a Options closure so a single client serves every region in the
	// run. Nil is tolerated and surfaces as ErrEnrichClientUnavailable.
	ResourceExplorer2 *resourceexplorer2.Client
	// APIGatewayV2 is the shared client for the API Gateway v2 hand-
	// rolled enricher (aws_apigatewayv2_stage). One client serves every
	// region in the run; the enricher applies a per-call region override
	// via an Options closure. Nil is tolerated and surfaces as
	// ErrEnrichClientUnavailable.
	APIGatewayV2 *apigatewayv2.Client
	// IAM is the shared client for IAM hand-rolled enrichers
	// (aws_iam_role_policy_attachment, aws_iam_policy, aws_iam_role,
	// aws_iam_role_policy). IAM is a global service, so no per-region
	// override is needed. Nil is tolerated and surfaces as
	// ErrEnrichClientUnavailable.
	IAM *iam.Client
	// Lambda is the shared client for the aws_lambda_function code
	// enricher (#661 follow-up). Lambda is regional; the enricher
	// applies a per-call region override via an Options closure so a
	// single client serves every region in the run. Nil is tolerated:
	// the lambda enricher still delegates to the Cloud Control path
	// and simply skips the GetFunction-sourced code attributes.
	Lambda *lambda.Client
	// AutoScaling is the shared client for the
	// aws_autoscaling_group_tag enricher (#482 final-2 push). Auto
	// Scaling is regional, so the enricher's fetch closure pins the
	// per-call region; one client serves every region in the run.
	// Nil is tolerated and surfaces as ErrEnrichClientUnavailable.
	AutoScaling *autoscaling.Client
	// WAFv2 is the shared client for the
	// aws_wafv2_web_acl_association enricher (#482 final-2 push).
	// WAFv2 is regional (CLOUDFRONT scope is handled on the
	// cloudfront_distribution side); the enricher applies its per-
	// call region override via an Options closure so a single client
	// serves every region in the run. Nil is tolerated and surfaces
	// as ErrEnrichClientUnavailable.
	WAFv2     *wafv2.Client
	AccountID string
}

// ErrEnrichClientUnavailable signals that the SDK client an enricher
// needs is nil on EnrichClients. Distinguishable from a real API error
// so EnrichAttributes can downgrade it to a per-resource warning (the
// type is silently un-enriched in the output) instead of a batch-fatal
// error. Mirrors gcpdiscover.ErrEnrichClientUnavailable.
var ErrEnrichClientUnavailable = errors.New("enrich: required SDK client unavailable on EnrichClients")

// isThrottleError reports whether err is an AWS rate-limit / throttling
// signal. It unwraps to a smithy.APIError (the same pattern isAPIErrorCode
// uses — see sdkonly_helpers.go) and matches the documented throttle
// ErrorCode() values across services, and additionally treats an HTTP
// 429 (Too Many Requests) or 503 (Slow Down / Service Unavailable) as a
// throttle via *smithyhttp.ResponseError.
//
// nil err returns false; a non-AWS error returns false.
func isThrottleError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ThrottlingException",
			"Throttling",
			"ThrottledException",
			"RequestThrottled",
			"RequestThrottledException",
			"RequestLimitExceeded",
			"TooManyRequestsException",
			"ProvisionedThroughputExceededException",
			"TransactionInProgressException",
			"SlowDown",
			"EC2ThrottledException":
			return true
		}
	}
	var httpErr *smithyhttp.ResponseError
	if errors.As(err, &httpErr) {
		switch httpErr.HTTPStatusCode() {
		case 429, 503:
			return true
		}
	}
	return false
}

// enrichWithRetry calls fn and, if it returns a throttle error (per
// isThrottleError), retries it under an exponential backoff with jitter
// — up to enrichRetryMaxAttempts total attempts. A nil result, a
// non-throttle error, or exhausting the attempt budget returns
// immediately. The backoff sleep is select-cancellable on ctx.Done();
// on cancel the last error is returned.
//
// This is a BACKSTOP layered ON TOP of the AWS SDK adaptive retryer
// (aws.RetryModeAdaptive, discoverRetryMaxAttempts — configured on the
// aws.Config in cmd/insideout-import/discover.go). When the caller built
// the EnrichClients SDK clients from a Config carrying that retryer, a
// throttle is usually absorbed below this layer and this loop rarely
// fires. It exists because EnrichClients are caller-supplied: a caller
// may construct clients without the adaptive retryer, in which case this
// loop is the only throttle protection. (The GCP sibling has no
// client-level adaptive retryer at all, so there this loop is the
// primary defense.)
//
// Re-running a soft-failed Enrich is safe: per-type enrichers marshal
// ir.Attrs atomically, so a retried call simply overwrites the prior
// (failed) attempt's partial state.
func enrichWithRetry(ctx context.Context, fn func() error) error {
	var err error
	backoff := enrichRetryBaseDelay
	for attempt := 1; ; attempt++ {
		err = fn()
		if err == nil || !isThrottleError(err) || attempt >= enrichRetryMaxAttempts {
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
// resource whose Identity.Type has a registered enricher. Resources of
// types without a registered enricher are left untouched; the caller
// can detect this via len(ir.Attrs) == 0 on return.
//
// The per-resource enrichers run concurrently under a bounded
// errgroup (defaultEnrichConcurrency workers) so a large account's
// cloud-API round-trips overlap instead of running strictly serially.
// Each goroutine writes a distinct irs[i] element, so the fan-out is
// data-race-free; the group is used purely as a bounded worker pool and
// never fails early. Progress emission and error aggregation happen in a
// SECOND serial pass after the workers finish, walking idx in sorted
// order — so emit order and error-join order remain exactly as
// deterministic as the old serial implementation, regardless of which
// worker completes first.
//
// Errors are accumulated per-resource and surfaced together at the end
// so a single mid-batch failure doesn't lose results from earlier
// successful enrichments — a partial result is more useful than no
// result. Two error kinds are downgraded to progress.ServiceWarn events
// (and not included in the returned error): ErrEnrichClientUnavailable,
// which reflects caller-side configuration rather than an API failure,
// and ErrNotFound, which is the expected per-resource outcome when the
// resource (or a sub-resource the enricher reads) genuinely does not
// exist — an S3 sub-resource that is unconfigured on its bucket, a Cloud
// Control GetResource that 404s (issue #654). The returned error wraps
// the joined per-resource errors for the remaining real failures;
// callers may inspect via errors.Is / errors.As.
//
// Every enriched resource has its Identity.EnrichmentStatus /
// EnrichErrors stamped: EnrichmentStatusFull on success,
// EnrichmentStatusFailed (with the error text in EnrichErrors) on any
// failure including the two downgraded kinds. This gives callers a
// machine-readable per-resource signal that does not depend on
// inspecting the returned joined error or on the absence of Attrs.
//
// emitter receives ItemFound per successfully enriched resource and
// ServiceWarn per ErrEnrichClientUnavailable. The standard
// (ServiceStart, ServiceFinish) pair brackets the whole batch under
// enrichServiceSlug. A nil emitter falls back to progress.NopEmitter.
//
// Mirrors gcpdiscover.GCPDiscoverer.EnrichAttributes — same dispatch
// order (sort by Identity.Type then Identity.Address), same error
// aggregation, same progress semantics. Symmetric APIs across clouds
// keep the consumer-side code (reliable's buildEnrichedAWSImports /
// buildEnrichedGCSImports) a one-liner per cloud.
func (a *AWSDiscoverer) EnrichAttributes(ctx context.Context, irs []imported.ImportedResource, clients EnrichClients, emitter progress.Emitter) error {
	if emitter == nil {
		emitter = progress.NopEmitter{}
	}
	stageStart := time.Now()
	emitter.ServiceStart(enrichServiceSlug, "")
	defer func() { emitter.ServiceFinish(enrichServiceSlug, "", len(irs), time.Since(stageStart)) }()

	// Dispatch in deterministic order so progress events and any
	// emitted warnings are stable across runs. Matches the GCP
	// (type, address) ordering.
	idx := make([]int, 0, len(irs))
	for i := range irs {
		if _, ok := a.byTypeEnricher[irs[i].Identity.Type]; ok {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(x, y int) bool {
		ix, iy := idx[x], idx[y]
		if irs[ix].Identity.Type != irs[iy].Identity.Type {
			return irs[ix].Identity.Type < irs[iy].Identity.Type
		}
		return irs[ix].Identity.Address < irs[iy].Identity.Address
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
	var g errgroup.Group
	g.SetLimit(defaultEnrichConcurrency)
	for pos, i := range idx {
		enr := a.byTypeEnricher[irs[i].Identity.Type]
		g.Go(func() error {
			// Jitter only the initial burst: the first
			// defaultEnrichConcurrency goroutines start simultaneously
			// at t=0, so their opening AWS calls would otherwise land
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
	_ = g.Wait()

	// Second pass: walk idx in sorted order to inspect results,
	// emit progress events, and aggregate errors. Doing this serially
	// — and outside the goroutines — keeps emit order and error-join
	// order deterministic (progress.Emitter is not documented as
	// concurrency-safe) and identical to the old serial behaviour.
	var errs []error
	for pos, i := range idx {
		err := results[pos]
		switch {
		case err == nil:
			// Per-type enrichers marshal Attrs atomically today, so a
			// nil return means Full. The Partial state is reserved for
			// a future multi-call enricher (see issue #471).
			irs[i].Identity.EnrichmentStatus = imported.EnrichmentStatusFull
			irs[i].Identity.EnrichErrors = nil
			emitter.ItemFound(enrichServiceSlug, irs[i].Identity.Region, irs[i].Identity.Type, irs[i].Identity.ImportID)
		case errors.Is(err, ErrEnrichClientUnavailable):
			// Client-side configuration failure — surface as a warn
			// but don't accumulate as an error. Mirrors the GCP-side
			// downgrade semantics. The typed signal on Identity lets
			// downstream consumers distinguish this from a happy
			// Identity-only IR (issue #471).
			irs[i].Identity.EnrichmentStatus = imported.EnrichmentStatusFailed
			irs[i].Identity.EnrichErrors = append(irs[i].Identity.EnrichErrors, err.Error())
			emitter.ServiceWarn(enrichServiceSlug, "", fmt.Sprintf("%s/%s: %v", irs[i].Identity.Type, irs[i].Identity.Address, err))
		case errors.Is(err, ErrNotFound):
			// The resource ID parsed but the cloud-side resource — or a
			// sub-resource the enricher reads — does not exist: an S3
			// sub-resource genuinely unconfigured on its bucket, a Cloud
			// Control GetResource that 404s, and so on. This is an
			// expected per-resource outcome, not a batch failure, so it
			// is downgraded to a warn and NOT accumulated into errs
			// (issue #654). The typed EnrichmentStatusFailed marker lets
			// the composer's drop-uncomposable filter elide the resource
			// and lets callers flag it without inspecting Attrs.
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
