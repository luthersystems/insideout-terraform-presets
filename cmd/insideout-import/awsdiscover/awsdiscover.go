// Package awsdiscover holds the AWS-side per-resource-type discoverers
// used by the insideout-import discover subcommand. Each discoverer
// issues read-only AWS SDK calls against the operator's account and
// returns Phase 2 imported.ImportedResource entries — no terraform-exec,
// no HCL generation. Stage 2b layers `terraform plan -generate-config-out`
// on top of this manifest to produce the actual .tf files.
//
// Originally landed as Stage 2a (#266); Stage 2c2 (#270) added bounded-
// concurrency errgroup fan-out inside the DynamoDB and Lambda discoverers
// (the only two with per-item tag API calls), gated by DefaultMaxConcurrency
// or a caller-supplied override on NewAWSDiscovererWithConcurrency.
//
// Discoverers in this package own narrow client interfaces so unit tests
// can mock the SDK boundary without depending on real AWS credentials.
// The aggregator (AWSDiscoverer) wires real SDK clients in production and
// fans out to the registered per-type discoverers concurrently under a
// bounded errgroup (DiscoverTypesConcurrency).
package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// DiscoverTypesConcurrency caps the number of per-service discoverers
// DiscoverTypes runs concurrently — the TYPE-level fan-out limit. Each
// selected Discoverer already has its own internal bounded fan-out for
// per-item SDK calls (tag fetches, GetResource walks); this constant
// bounds the service-level layer on top of that.
//
// NOT to be confused with DefaultMaxConcurrency (= 10), which is the
// PER-RESOURCE GetResource / tag-fetch fan-out cap inside a single
// discoverer. The two govern different layers: DiscoverTypesConcurrency
// is "how many resource TYPES discover at once", DefaultMaxConcurrency
// is "how many per-item SDK calls one type issues at once". A downstream
// consumer that fans out single-type DiscoverTypes calls itself (e.g.
// reliable's streaming discover path, which issues one call per type to
// receive per-type results as they land) should bound its own fan-out by
// THIS constant, not DefaultMaxConcurrency — using the per-resource cap
// there would 2.5× the intended type-level control-plane call rate (the
// confusion that produced reliable#2065 codex round 2).
//
// Originally 8 (#629); lowered to 4 (#632) after staging hit a
// ThrottlingException from CloudControl ListResources during the
// broad scan. 8 simultaneous t=0 kickoffs, each with internal
// per-service fan-out, exceeded CloudControl's per-account rate
// budget. 4 still gives ~4× wall-time savings over sequential
// without multiplying account-wide QPS by the registered-type count.
// See also: defaultDiscoverStartupJitterMax for the per-goroutine
// jitter applied before the first AWS call.
const DiscoverTypesConcurrency = 4

// defaultDiscoverStartupJitterMax bounds the random sleep applied
// before each per-service goroutine's first AWS call. Without
// jitter, all N goroutines kick off at t=0 and their first
// ListResources / Describe* calls land in the same burst, lighting
// up the per-region CloudControl rate-limiter (#632). 500ms gives
// the SDK retryer's adaptive token bucket room to spread the load
// without materially extending wall time (a typical broad scan
// takes tens of seconds).
const defaultDiscoverStartupJitterMax = 500 * time.Millisecond

// ErrNotSupported signals that a discoverer cannot resolve a given ID
// (e.g. an ARN whose service portion does not match this discoverer's
// resource type, or an ID shape this discoverer does not parse). Stage
// 2c3's dep-chase loop converts ErrNotSupported into an operator-facing
// warning rather than a fatal error.
var ErrNotSupported = errors.New("discoverer does not support this ID")

// ErrNotFound signals that the ID parsed correctly but the resource
// does not exist in the operator's account / region (or returned a
// no-such-entity error from the underlying SDK). Stage 2c3 surfaces
// this as a warning too — the operator can decide whether to remove
// the dangling reference or rerun once the resource is created.
var ErrNotFound = errors.New("resource not found")

// Discoverer is the per-resource-type contract. Each implementation handles
// one Terraform type (e.g. "aws_sqs_queue") and returns []imported.ImportedResource
// directly — no intermediate flat shape, per #189.
//
// The bulk Discover takes a DiscoverArgs struct (#291): Project, Regions,
// TagSelectors, AccountID. Per-region SDK clients are constructed inside
// each implementation so global services (IAM, S3) can ignore Regions
// without polluting the aggregator with per-cloud branching.
//
// DiscoverByID stays on the legacy 4-arg shape because single-resource
// lookups have no meaningful multi-region or tag-selector semantics —
// dep-chase resolves one ID at a time, in one region, with no filters.
type Discoverer interface {
	// ResourceType returns the Terraform type this discoverer covers, e.g.
	// "aws_sqs_queue".
	ResourceType() string
	// Discover performs read-only SDK calls and returns one ImportedResource
	// per matched cloud resource. Implementations populate Identity and set
	// Tier=TierImportedFlat, Source=SourceImporter on each entry.
	Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error)
	// DiscoverByID looks up a single resource by its native ID (an ARN or
	// the natural key the discoverer's Discover method emits — queue URL,
	// table name, log group name, etc.). Used by Stage 2c3's dep-chase
	// loop when the generated.tf references a resource not in the
	// original import set. Returns (zero, ErrNotSupported) for an ID
	// shape this discoverer does not parse, (zero, ErrNotFound) for a
	// well-formed ID whose underlying resource does not exist, or any
	// other error for a real SDK failure.
	DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error)
}

// DefaultMaxConcurrency is the per-discoverer fan-out limit applied when
// the caller does not request a specific value. 10 is the empirical sweet
// spot from the audit in #270 — high enough to keep a few-thousand-resource
// account scan under a minute, low enough that the SDK's adaptive retryer
// can absorb transient Throttling without exhausting the configured retry
// budget.
const DefaultMaxConcurrency = 10

// AWSDiscoverer aggregates the per-type discoverers and fans out a single
// DiscoverTypes call across all of them. Construct with NewAWSDiscoverer
// in production; tests can build it directly with mock discoverers.
//
// defaultRegion is captured from the construction-time aws.Config and
// substituted into args.Regions when the operator passes none —
// preserves the pre-#291 single-region behavior so callers that haven't
// migrated to --regions still scan the configured-region.
type AWSDiscoverer struct {
	byType        map[string]Discoverer
	defaultRegion string
	// cfg is the construction-time aws.Config. DiscoverTypes uses it to
	// build per-region EC2 clients for the resolveVPCChildVPCIDs
	// augmentation pass (#651), which needs live Describe* calls the
	// per-type discoverers do not make.
	cfg aws.Config
	// rgtPrefetcher is the optional RGT (Resource Groups Tagging API)
	// pre-pass run once per DiscoverTypes call. Defaults to a real
	// prefetcher constructed from the aws.Config; tests can swap in a
	// fake or the noopRGTPrefetcher. Issued in DiscoverTypes before
	// the per-type fan-out so opt-in discoverers
	// (cloudControlDiscoverer) can skip their own per-type
	// ListResources when Prefetch returns a cache hit. See #406.
	rgtPrefetcher rgtPrefetcher
	// byTypeEnricher carries the per-Terraform-type SDK attribute
	// enrichers (#457). Each entry is a sibling to the byType
	// discoverer of the same name and populates ir.Attrs (the typed
	// Layer 1 payload) so callers can produce decision-#34-clean HCL
	// via composer.EmitImportedTF without needing the terraform-driven
	// Stage 2b path. Types without an entry here are silently skipped
	// by EnrichAttributes — the full enricher rollout follows the
	// existing per-type ordering one PR at a time. Mirrors
	// gcpdiscover.GCPDiscoverer.byTypeEnricher (presets#403).
	byTypeEnricher map[string]AttributeEnricher
	// startupJitter caps the random per-goroutine sleep applied
	// before the first AWS call inside DiscoverTypes (#632). Set to
	// defaultDiscoverStartupJitterMax by the production constructors;
	// tests override to 0 (disable) or a tiny value to keep wall time
	// short while still asserting jitter is applied.
	startupJitter time.Duration
	// jitterSample produces the per-goroutine startup delay. The
	// production constructor wires a closure that draws a uniform
	// sample in [0, startupJitter); tests inject a deterministic
	// sequence so jitter assertions don't depend on global rand's
	// seeding policy (math/rand auto-seeds since Go 1.20, but a
	// future move to math/rand/v2 or an explicit seed would break a
	// statistical assertion). A nil sampler defaults inside
	// DiscoverTypes to the same closure shape.
	jitterSample func() time.Duration
	// jitterSleep is the seam DiscoverTypes calls before each
	// goroutine's first per-service Discover. Defaults to
	// time.Sleep; tests inject a fake that records the per-goroutine
	// sleep durations so jitter behavior can be asserted without
	// spinning real wall time. Each call receives the sampled
	// duration for that goroutine.
	jitterSleep func(time.Duration)
}

// defaultJitterSample returns a uniformly-random delay in
// [0, a.startupJitter). Returns 0 when startupJitter <= 0, so callers
// don't have to guard rand.Int63n against a zero/negative bound (it
// panics on n <= 0). Used as the default for AWSDiscoverer.jitterSample.
func (a *AWSDiscoverer) defaultJitterSample() time.Duration {
	if a.startupJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(a.startupJitter)))
}

// NewAWSDiscoverer wires up the production set of AWS discoverers with the
// default per-type fan-out limit. Equivalent to
// NewAWSDiscovererWithConcurrency(cfg, DefaultMaxConcurrency).
func NewAWSDiscoverer(cfg aws.Config) *AWSDiscoverer {
	return NewAWSDiscovererWithConcurrency(cfg, DefaultMaxConcurrency)
}

// NewAWSDiscovererWithConcurrency wires up the production set of AWS
// discoverers — the 5 Phase 1 types (SQS, DynamoDB, CloudWatch Logs,
// Secrets Manager, Lambda) plus the 4 dep-chase reference types added
// in Stage 2c3 (#271): IAM role, IAM policy, KMS key, S3 bucket. All
// discoverers share the same aws.Config; per-type SDK clients are
// constructed inside each discoverer. maxConcurrency is the upper
// bound on per-resource tag-fanout calls inside the DynamoDB and
// Lambda discoverers (the only two with per-item API fan-out today).
// The other discoverers either filter server-side (SecretsManager) or
// only issue a single List/page call and ignore the limit.
//
// A non-positive maxConcurrency falls back to DefaultMaxConcurrency rather
// than serializing — callers should validate flag input upstream and fail
// loudly there. The fallback exists only as a safety net for direct
// programmatic callers.
func NewAWSDiscovererWithConcurrency(cfg aws.Config, maxConcurrency int) *AWSDiscoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	// Bucket C (#406): the four AWS types that still use hand-rolled
	// per-service SDK discoverers — Cloud Control either can't READ
	// them (apigatewayv2_stage) or their listing semantics require a
	// behavior the unified path can't model (bedrock_guardrail's
	// per-version fan-out; resourceexplorer2_*'s cross-region dedup
	// quirk, #336). Everything else is registered below via the
	// cloudControlTypeConfigs loop.
	byType := map[string]Discoverer{
		"aws_apigatewayv2_stage": newAPIGatewayV2StageDiscoverer(cfg, maxConcurrency),
		"aws_bedrock_guardrail":  newBedrockGuardrailDiscoverer(cfg, maxConcurrency),
		// Bucket C non-CC (#466 follow-up): Cloud Control does not
		// know the CFN type
		// AWS::Bedrock::ModelInvocationLoggingConfiguration
		// (TypeNotFoundException on `cloudcontrol get-resource`).
		// Native bedrock SDK end-to-end is the only working path;
		// the framework's per-item fan-out through CC GetResource
		// cannot rescue it.
		"aws_bedrock_model_invocation_logging_configuration": newBedrockModelInvocationLoggingConfigurationDiscoverer(cfg, maxConcurrency),
		"aws_resourceexplorer2_index":                        newResourceExplorer2IndexDiscoverer(cfg, maxConcurrency),
		"aws_resourceexplorer2_view":                         newResourceExplorer2ViewDiscoverer(cfg, maxConcurrency),
		// Bucket C non-CC (#466 follow-up): Cloud Control returns
		// UnsupportedActionException on READ for
		// AWS::ServiceDiscovery::PrivateDnsNamespace; neither the
		// unified cloudControlDiscoverer nor SDKLister can resolve
		// the type. Native servicediscovery SDK end-to-end with a
		// Route53 GetHostedZone hop to recover the VPC id (TF
		// import format is "<namespace_id>:<vpc_id>"; the
		// servicediscovery SDK never surfaces VpcId).
		"aws_service_discovery_private_dns_namespace": newServiceDiscoveryPrivateDNSNamespaceDiscoverer(cfg, maxConcurrency),
	}
	// Cloud Control-routed types (#406): each entry in
	// cloudControlTypeConfigs becomes one cloudControlDiscoverer
	// registration. Tag filtering rides on the RGT prefetcher (run
	// once per DiscoverTypes call); per-type ListResources is only
	// invoked on cache miss. See cloudcontrol_types.go and
	// cloudcontrol_discoverer.go.
	for _, ccCfg := range cloudControlTypeConfigs {
		byType[ccCfg.TFType] = newCloudControlDiscoverer(ccCfg, cfg, maxConcurrency)
	}
	// SDK-only sub-resource types (Bundle 14k1, #452): for Terraform
	// types that have no Cloud Control representation (e.g. S3 bucket
	// sub-resources that CFN models as inline bucket properties rather
	// than standalone resource types). Each entry in
	// sdkOnlySubresourceTypeConfigs becomes one
	// sdkOnlySubresourceDiscoverer registration. Parent enumeration
	// reuses the parent's RGT cache when SkipProjectTagFilter is unset
	// or falls back to a per-type ListParents SDK call. See
	// sdkonly_subresource_discoverer.go and sdkonly_s3.go.
	for _, soCfg := range sdkOnlySubresourceTypeConfigs {
		byType[soCfg.TFType] = newSDKOnlySubresourceDiscoverer(soCfg, cfg, maxConcurrency)
	}
	// Per-type SDK attribute enrichers (#457). Each entry is a sibling
	// to the byType discoverer of the same name and populates ir.Attrs
	// (the typed Layer 1 payload). Types without an entry here are
	// silently skipped by EnrichAttributes — the full enricher rollout
	// follows the existing per-type ordering one PR at a time. Mirrors
	// gcpdiscover.GCPDiscoverer (presets#403).
	//
	// Hand-rolled enrichers win as overrides over the generic Cloud
	// Control fallback below — they produce strictly more correct
	// payloads today (primary-name field aliasing, ARN normalization,
	// tag-overlay fetches), per the PoC report in
	// .tmp/cloud-control-enricher-poc.md. The Cloud Control enricher
	// fills in the long tail of CC-routed types that have no hand-rolled
	// override.
	//
	// (#493) S3 enricher landed: aws_s3_bucket is wired below. The
	// hand-rolled enricher is the reliable path; once the Cloud Control
	// unified enricher (#490) proves out S3 coverage, this registration
	// can flip to the unified path and s3_bucket_enrich.go can be
	// retired (the framework preserves the override capability for any
	// per-type quirk the unified path doesn't model).
	byTypeEnricher := map[string]AttributeEnricher{
		"aws_apigatewayv2_stage": newAPIGatewayV2StageEnricher(),
		// Final-2 enricher push (#482) — closes the last two SDK-only
		// sub-resource discoverers (aws_autoscaling_group_tag,
		// aws_wafv2_web_acl_association) that lacked a hand-rolled
		// AttributeEnricher. Each uses a per-binding lookup RPC
		// (DescribeTags filtered server-side, GetWebACLForResource)
		// rather than rerunning the discoverer's fan-out shape; one
		// SDK call per enriched row.
		"aws_autoscaling_group_tag":                          newAutoscalingGroupTagEnricher(),
		"aws_bedrock_guardrail":                              newBedrockGuardrailEnricher(),
		"aws_bedrock_model_invocation_logging_configuration": newBedrockModelInvocationLoggingConfigurationEnricher(),
		// aws_cloudwatch_log_group retired in #502 — handled by the
		// generic Cloud Control + Normalizer path below
		// (chain(renameField LogGroupName→Name, synthIDFromField Name,
		// trimARNStar Arn, flattenTagList Tags) reaches 100% exact field
		// match with the retired hand-rolled enricher; see
		// cloudcontrol_types.go for the wiring).
		"aws_dynamodb_contributor_insights": newDDBContributorInsightsEnricher(),
		"aws_dynamodb_table":                newDynamoDBTableEnricher(),
		// aws_iam_policy / aws_iam_role / aws_iam_role_policy: hand-
		// rolled overrides (#661 + follow-up). The generic Cloud Control
		// enricher leaves these resources' REQUIRED JSON-document
		// arguments (`policy`, `assume_role_policy`) empty — the CFN
		// schemas treat the policy document as create-time input, not a
		// stably-readable property, so GetResource omits it. Each
		// enricher reads the document via the matching IAM Get* API
		// instead. See iam_policy_enrich.go / iam_role_enrich.go /
		// iam_role_policy_enrich.go.
		"aws_iam_policy":                 newIAMPolicyEnricher(),
		"aws_iam_role":                   newIAMRoleEnricher(),
		"aws_iam_role_policy":            newIAMRolePolicyEnricher(),
		"aws_iam_role_policy_attachment": newIAMRolePolicyAttachmentEnricher(),
		"aws_resourceexplorer2_index":    newResourceExplorer2IndexEnricher(),
		"aws_resourceexplorer2_view":     newResourceExplorer2ViewEnricher(),
		"aws_s3_bucket":                  newS3BucketEnricher(),
		// aws_s3_bucket_policy: hand-rolled override (#661 follow-up) —
		// the REQUIRED `policy` argument has the same CC-path gap; the
		// enricher reads it via s3:GetBucketPolicy. See
		// s3_bucket_policy_enrich.go.
		"aws_s3_bucket_policy": newS3BucketPolicyEnricher(),
		// S3 bucket sub-resource enrichers — all five share the
		// EnrichClients.S3 client; the per-bucket GetBucket* SDK calls
		// fan out one-at-a-time and produce the typed Layer-1 payload
		// for each sub-resource type.
		"aws_s3_bucket_lifecycle_configuration":              newS3BucketLifecycleConfigurationEnricher(),
		"aws_s3_bucket_ownership_controls":                   newS3BucketOwnershipControlsEnricher(),
		"aws_s3_bucket_public_access_block":                  newS3BucketPublicAccessBlockEnricher(),
		"aws_s3_bucket_server_side_encryption_configuration": newS3BucketServerSideEncryptionConfigurationEnricher(),
		"aws_s3_bucket_versioning":                           newS3BucketVersioningEnricher(),
		"aws_secretsmanager_secret":                          newSecretsManagerSecretEnricher(),
		"aws_service_discovery_private_dns_namespace":        newServiceDiscoveryPrivateDNSNamespaceEnricher(),
		"aws_wafv2_web_acl_association":                      newWAFv2WebACLAssociationEnricher(),
	}
	// HYBRID Cloud Control fallback (#490 steps 1+2): register one
	// cloudControlEnricher for every TF type in cloudControlTypeConfigs
	// that doesn't already have a hand-rolled override above. Hand-rolled
	// wins; the loop iterates the same config the discoverer uses so the
	// enricher coverage stays in lockstep with the listing coverage.
	//
	// The enricher's GetResource callback defaults to nil and is
	// resolved at Enrich time from EnrichClients.CloudControl. Callers
	// constructing EnrichClients without a CloudControl client see a
	// per-resource ErrEnrichClientUnavailable warning (not a batch-fatal
	// error) so a partial-credentials run still produces useful output
	// from the hand-rolled enrichers.
	for _, ccCfg := range cloudControlTypeConfigs {
		if _, has := byTypeEnricher[ccCfg.TFType]; has {
			continue
		}
		// #501 — pass through the per-type Normalizer (nil for types
		// whose CFN shape already matches the camelToSnake projection).
		// #582 — wrap with the generic computed-only filter LAST so
		// every CC-routed type benefits from decision-#5 elision
		// without per-type opt-in. The filter is a no-op for types
		// whose CFN payload doesn't include any computed-only fields
		// (and fail-open for the rare unregistered-in-`generated`
		// type, so a wiring gap can't cause a runtime regression).
		// Hand-rolled enricher overrides (above) take precedence over
		// this loop, so the filter doesn't perturb the SDK-only
		// enrichers' map<Type> output.
		normalizer := chain(ccCfg.Normalizer, stripComputedOnlyForType(ccCfg.TFType))
		byTypeEnricher[ccCfg.TFType] = newCloudControlEnricherWithNormalizer(
			ccCfg.TFType, ccCfg.CloudFormationType, nil, normalizer,
		)
	}
	// aws_lambda_function: composite enricher (#661 follow-up). Unlike
	// the policy-document enrichers above, lambda is NOT a full hand-
	// rolled override — the Cloud Control path already maps the large
	// AWS::Lambda::Function surface well. The composite delegates the
	// bulk to the CC enricher the loop just registered, then issues one
	// lambda:GetFunction call to recover the code attributes CC cannot
	// read back (`image_uri` / `package_type` for container-image
	// functions). Wrapping the already-built CC enricher keeps the
	// composite in lockstep with any future CC lambda normalizer change.
	if cc, ok := byTypeEnricher["aws_lambda_function"]; ok {
		byTypeEnricher["aws_lambda_function"] = newLambdaFunctionEnricher(cc)
	}
	// aws_cloudfront_function: composite enricher (#665). Same shape as
	// the lambda composite — delegates the bulk mapping to the CC
	// enricher, then overlays the required `code` + `runtime`
	// attributes CloudControl GetResource does not return. Unlike
	// lambda, the overlay is mandatory (both are required arguments).
	if cc, ok := byTypeEnricher["aws_cloudfront_function"]; ok {
		byTypeEnricher["aws_cloudfront_function"] = newCloudfrontFunctionEnricher(cc)
	}
	disc := &AWSDiscoverer{
		defaultRegion:  cfg.Region,
		cfg:            cfg,
		byType:         byType,
		rgtPrefetcher:  newRealRGTPrefetcher(cfg),
		byTypeEnricher: byTypeEnricher,
		startupJitter:  defaultDiscoverStartupJitterMax,
		jitterSleep:    time.Sleep,
	}
	disc.jitterSample = disc.defaultJitterSample
	return disc
}

// serviceSlugByTFType maps a Terraform resource type to the short,
// stable progress-event service slug (#295). The slug appears in the
// `service` field of every progress.Event emitted by the per-service
// discoverer; downstream consumers (reliable agent-API SSE translator)
// pivot UI rows on these strings, so they're locked here as a single
// source of truth. The names match the package directory / file
// convention already used in this package (sqs.go, dynamodb.go,
// cloudwatchlogs.go, etc.) so a regression that switches a per-service
// file's slug will diverge from the file name and be obvious in review.
var serviceSlugByTFType = map[string]string{
	// Bucket C — hand-rolled. Slugs must match the per-discoverer
	// ServiceStart/Finish strings inside each *.go file.
	"aws_apigatewayv2_stage":                             "apigatewayv2_stage",
	"aws_bedrock_guardrail":                              "bedrock_guardrail",
	"aws_bedrock_model_invocation_logging_configuration": "bedrock_model_invocation_logging_configuration",
	"aws_resourceexplorer2_index":                        "resourceexplorer2_index",
	"aws_resourceexplorer2_view":                         "resourceexplorer2_view",
	"aws_service_discovery_private_dns_namespace":        "service_discovery_private_dns_namespace",
}

// ServiceSlug returns the progress-event slug for a Terraform resource
// type, falling back to the type itself when no slug is registered. For
// Cloud Control-routed types (the bulk of the registry post-#406),
// ServiceSlug consults cloudControlTypeConfigs first so the slug stays
// in lockstep with the Slug field on each cloudControlConfig entry.
// Falling back (rather than panicking) keeps the Emitter safe to call
// from any Discoverer, including test-only ones a future contributor
// might register without updating the slug map.
func ServiceSlug(tfType string) string {
	if s, ok := serviceSlugCombined[tfType]; ok {
		return s
	}
	return tfType
}

// serviceSlugCombined merges serviceSlugByTFType (4 Bucket-C entries)
// with cloudControlTypeConfigs slugs and sdkOnlySubresourceTypeConfigs
// slugs into one O(1) lookup table. Built once at package init so
// ServiceSlug avoids the O(n) scan that would otherwise repeat per
// Emitter event.
var serviceSlugCombined = func() map[string]string {
	out := make(map[string]string, len(serviceSlugByTFType)+len(cloudControlTypeConfigs)+len(sdkOnlySubresourceTypeConfigs))
	for k, v := range serviceSlugByTFType {
		out[k] = v
	}
	for _, cfg := range cloudControlTypeConfigs {
		out[cfg.TFType] = cfg.Slug
	}
	for _, cfg := range sdkOnlySubresourceTypeConfigs {
		out[cfg.TFType] = cfg.Slug
	}
	return out
}()

// SupportedTypes returns the registered Terraform types in lexicographic
// order. Used by the CLI for default --resource-types and validation.
func (a *AWSDiscoverer) SupportedTypes() []string {
	out := make([]string, 0, len(a.byType))
	for t := range a.byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// DiscoverByID dispatches a per-ID lookup to the discoverer registered
// for the given Terraform type. Used by Stage 2c3's dep-chase loop.
// Returns ErrNotSupported if no discoverer is registered for the
// requested type — dep-chase converts that into a warning so the
// operator can decide whether to remove the dangling reference or add
// a discoverer for the missing type.
func (a *AWSDiscoverer) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	d, ok := a.byType[tfType]
	if !ok {
		return imported.ImportedResource{}, fmt.Errorf("no discoverer registered for %q: %w", tfType, ErrNotSupported)
	}
	return d.DiscoverByID(ctx, id, region, accountID)
}

// DiscoverTypes runs the selected per-service discoverers concurrently
// under a bounded errgroup (default concurrency:
// DiscoverTypesConcurrency). Per-item concurrency already lives
// inside each discoverer (tag-fanout, sub-resource walks); adding
// service-level fan-out shortens wall time for multi-service imports
// without changing per-service throttle behavior. Unknown type names
// are still reported as a single error containing all invalid names
// (not interleaved with partial results) so the operator sees the full
// set of misspellings in one shot.
//
// Selection order is preserved in the returned slice: each goroutine
// writes into its own pre-allocated index, so a flatten-after-Wait
// keeps results deterministic without a mutex. Errors propagate via
// errgroup's fail-fast semantics — the first per-service error cancels
// sibling goroutines and is returned wrapped as
// "<ResourceType>: <err>", matching the pre-parallelization shape.
//
// Multi-region (#291): each per-service Discover loops args.Regions
// internally and builds per-region SDK clients via the configured
// aws.Config; global services (IAM role/policy, S3) ignore Regions. An
// empty args.Regions defaults to the configured-region of the
// aws.Config inside each per-service implementation, preserving the
// pre-#291 single-region behavior.
func (a *AWSDiscoverer) DiscoverTypes(ctx context.Context, types []string, args DiscoverArgs) ([]imported.ImportedResource, error) {
	if len(types) == 0 {
		types = a.SupportedTypes()
	}
	if len(args.Regions) == 0 {
		args.Regions = []string{a.defaultRegion}
	}
	// Resolve a nil Emitter once here so per-service Discover bodies
	// can call args.Emitter.* unconditionally. The progress package's
	// NopEmitter is zero-overhead.
	if args.Emitter == nil {
		args.Emitter = progress.NopEmitter{}
	}

	var unknown []string
	selected := make([]Discoverer, 0, len(types))
	for _, t := range types {
		d, ok := a.byType[t]
		if !ok {
			unknown = append(unknown, t)
			continue
		}
		selected = append(selected, d)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown resource type(s): %v (supported: %v)", unknown, a.SupportedTypes())
	}

	// RGT pre-pass: one call per region returns every tag-filtered ARN
	// across all services, bucketed by CloudFormation type. Per-type
	// discoverers that opt in read this cache via
	// args.RGTCacheForCFN/RGTCacheForGlobalCFN; per-type cache misses
	// fall through to the existing ListResources path. Tests can set
	// rgtPrefetcher to noopRGTPrefetcher{} to short-circuit. See #406.
	if a.rgtPrefetcher != nil {
		cache, err := a.rgtPrefetcher.Prefetch(ctx, args.Regions, args)
		if err != nil {
			return nil, fmt.Errorf("rgt prefetch: %w", err)
		}
		args = args.withRGTCache(cache)
	}

	stageStart := time.Now()
	results := make([][]imported.ImportedResource, len(selected))

	// Per-type progress (#699): when args.Emitter additionally
	// implements TypeProgressEmitter (the pkg/imported facade's bridge
	// does; the wire JSONEmitter / NopEmitter do not), fire one TypeDone
	// per type as its parallel Discover lands so a facade consumer can
	// stream a real "N of total types" progress signal instead of a
	// cosmetic timer. The bridge serializes these concurrent calls under
	// its own lock; a non-TypeProgressEmitter Emitter leaves typeSink nil
	// and the path is skipped (byte-for-byte unchanged behavior).
	typeSink, _ := args.Emitter.(progress.TypeProgressEmitter)
	totalTypes := len(selected)
	emitTypeDone := func(tfType string, found int) {
		if typeSink != nil {
			typeSink.TypeDone(progress.TypeProgress{
				Phase:  "discover",
				TFType: tfType,
				Found:  found,
				Total:  totalTypes,
			})
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(DiscoverTypesConcurrency)
	// Per-goroutine startup jitter (#632). Without this, all N
	// service goroutines unblock at t=0 and their first
	// ListResources / Describe* calls land in the same burst,
	// lighting up the per-region CloudControl rate-limiter. Each
	// goroutine sleeps a uniformly-random duration in
	// [0, a.startupJitter) before its first AWS call, spreading the
	// initial wave so per-service requests don't all align at t=0.
	//
	// Sampling is delegated to a.jitterSample (defaults to a uniform
	// draw in [0, a.startupJitter)) and the actual sleep is performed
	// via a.jitterSleep so tests can inject a deterministic sample
	// sequence + recorder without spinning real wall time.
	jitterSleep := a.jitterSleep
	if jitterSleep == nil {
		jitterSleep = time.Sleep
	}
	jitterSample := a.jitterSample
	if jitterSample == nil {
		jitterSample = a.defaultJitterSample
	}
	for i, d := range selected {
		delay := jitterSample()
		g.Go(func() error {
			if delay > 0 {
				jitterSleep(delay)
			}
			// Cancellation check after jitter so a fail-fast sibling
			// can short-circuit the slowest-jittered goroutines
			// before they fire any AWS calls.
			if err := gctx.Err(); err != nil {
				return err
			}
			// Per-type deadline (#1787 reliable). When
			// args.PerTypeTimeout > 0, wrap the Discover call in a
			// child context so one slow / stalled SDK call can't hold
			// the whole gather hostage. On timeout we WARN-log and
			// emit an empty slice for this type — partial result is
			// strictly more useful than no result for a best-effort
			// survey. The errgroup itself never sees the timeout
			// error, so siblings keep running.
			//
			// Non-timeout errors still propagate through the errgroup
			// so a genuine API failure (Throttling, invalid creds)
			// continues to fail-fast as before.
			callCtx := gctx
			var cancel context.CancelFunc
			if args.PerTypeTimeout > 0 {
				callCtx, cancel = context.WithTimeout(gctx, args.PerTypeTimeout)
				defer cancel()
			}
			callStart := time.Now()
			entries, err := d.Discover(callCtx, args)
			if err != nil {
				// Distinguish "per-type budget expired" from "parent
				// context cancelled" (a fail-fast sibling). Only the
				// per-type case downgrades to a partial-result warn;
				// parent cancellation must still propagate so the
				// caller's deadline / fail-fast semantics work.
				if args.PerTypeTimeout > 0 &&
					errors.Is(err, context.DeadlineExceeded) &&
					gctx.Err() == nil {
					log.Printf("[awsdiscover] level=warn reason=per_type_timeout type=%s regions=%v elapsed_ms=%d budget_ms=%d",
						d.ResourceType(),
						args.Regions,
						time.Since(callStart).Milliseconds(),
						args.PerTypeTimeout.Milliseconds())
					results[i] = nil
					// This type completed (with a partial / empty
					// result); count it toward N-of-total so the
					// progress denominator still reaches the total.
					emitTypeDone(d.ResourceType(), 0)
					return nil
				}
				return fmt.Errorf("%s: %w", d.ResourceType(), err)
			}
			results[i] = entries
			emitTypeDone(d.ResourceType(), len(entries))
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var total int
	for _, r := range results {
		total += len(r)
	}
	all := make([]imported.ImportedResource, 0, total)
	for _, r := range results {
		all = append(all, r...)
	}
	// VPC-child VPC augmentation (#651): aws_internet_gateway and
	// aws_vpc_dhcp_options carry no VpcId in their Cloud Control model,
	// so the #650 in-memory join cannot find their parent aws_vpc. This
	// SDK-backed pass issues EC2 Describe* calls to recover the link and
	// stamps NativeIDs["vpc_id"] onto the matching resources, after which
	// resolveParentAddresses handles them as ordinary forward-edge FK
	// rules. A failure here must not abort an otherwise-successful
	// discovery run — the parent links are best-effort enrichment — so a
	// non-nil error is downgraded to a service warning.
	if err := resolveVPCChildVPCIDs(ctx, all, args.Regions, func(region string) ec2VPCChildAPI {
		return ec2.NewFromConfig(a.cfg, func(o *ec2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}); err != nil {
		args.Emitter.ServiceWarn("vpc_parent_resolve", "", err.Error())
	}
	// Parent-instance resolution (#650): now that the full cross-
	// discoverer set is assembled, join each discovered child to the
	// specific parent instance it belongs to and stamp the parent's
	// Terraform Address onto Identity.ParentAddress. Pure in-memory join
	// — no AWS calls.
	resolveParentAddresses(all)
	args.Emitter.StageFinish("discover", len(all), time.Since(stageStart))
	return all, nil
}
