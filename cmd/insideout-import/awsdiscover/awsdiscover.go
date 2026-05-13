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
// fans out to the registered per-type discoverers.
package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

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
	byType := map[string]Discoverer{
		"aws_sqs_queue":                       newSQSDiscoverer(cfg),
		"aws_dynamodb_table":                  newDynamoDBDiscoverer(cfg, maxConcurrency),
		"aws_cloudwatch_log_group":            newCloudWatchLogsDiscoverer(cfg),
		"aws_secretsmanager_secret":           newSecretsManagerDiscoverer(cfg),
		"aws_lambda_function":                 newLambdaDiscoverer(cfg, maxConcurrency),
		"aws_iam_role":                        newIAMRoleDiscoverer(cfg),
		"aws_iam_policy":                      newIAMPolicyDiscoverer(cfg),
		"aws_kms_key":                         newKMSDiscoverer(cfg),
		"aws_s3_bucket":                       newS3Discoverer(cfg),
		"aws_vpc":                             newVPCDiscoverer(cfg),
		"aws_subnet":                          newSubnetDiscoverer(cfg),
		"aws_security_group":                  newSecurityGroupDiscoverer(cfg),
		"aws_internet_gateway":                newInternetGatewayDiscoverer(cfg),
		"aws_nat_gateway":                     newNatGatewayDiscoverer(cfg),
		"aws_eip":                             newEIPDiscoverer(cfg),
		"aws_route_table":                     newRouteTableDiscoverer(cfg),
		"aws_network_acl":                     newNetworkACLDiscoverer(cfg),
		"aws_vpc_endpoint":                    newVPCEndpointDiscoverer(cfg),
		"aws_vpc_dhcp_options":                newVPCDHCPOptionsDiscoverer(cfg),
		"aws_network_interface":               newNetworkInterfaceDiscoverer(cfg),
		"aws_route53_zone":                    newRoute53ZoneDiscoverer(cfg),
		"aws_cloudfront_distribution":         newCloudFrontDistributionDiscoverer(cfg),
		"aws_db_instance":                     newDBInstanceDiscoverer(cfg),
		"aws_db_subnet_group":                 newDBSubnetGroupDiscoverer(cfg, maxConcurrency),
		"aws_db_parameter_group":              newDBParameterGroupDiscoverer(cfg, maxConcurrency),
		"aws_lb":                              newLBDiscoverer(cfg, maxConcurrency),
		"aws_lb_listener":                     newLBListenerDiscoverer(cfg, maxConcurrency),
		"aws_lb_target_group":                 newLBTargetGroupDiscoverer(cfg, maxConcurrency),
		"aws_bedrock_guardrail":               newBedrockGuardrailDiscoverer(cfg, maxConcurrency),
		"aws_opensearchserverless_collection": newOpenSearchServerlessCollectionDiscoverer(cfg, maxConcurrency),
		"aws_apigatewayv2_api":                newAPIGatewayV2APIDiscoverer(cfg, maxConcurrency),
		"aws_apigatewayv2_stage":              newAPIGatewayV2StageDiscoverer(cfg, maxConcurrency),
		"aws_eks_pod_identity_association":    newEKSPodIdentityDiscoverer(cfg, maxConcurrency),
		"aws_cloudwatch_event_rule":           newCloudWatchEventRuleDiscoverer(cfg, maxConcurrency),
		"aws_resourceexplorer2_index":         newResourceExplorer2IndexDiscoverer(cfg, maxConcurrency),
		"aws_resourceexplorer2_view":          newResourceExplorer2ViewDiscoverer(cfg, maxConcurrency),
	}
	// Cloud Control-routed types (Bundle 13): each entry in
	// cloudControlTypeConfigs becomes one cloudControlDiscoverer
	// registration. The cloudControlDiscoverer carries the per-type
	// TypeName + extractors in cfg so this loop is the only place new
	// Cloud Control-covered types need wiring. See cloudcontrol_types.go
	// for the registry and cloudcontrol_discoverer.go for the implementation.
	for _, ccCfg := range cloudControlTypeConfigs {
		byType[ccCfg.TFType] = newCloudControlDiscoverer(ccCfg, cfg, maxConcurrency)
	}
	return &AWSDiscoverer{
		defaultRegion: cfg.Region,
		byType:        byType,
	}
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
	"aws_sqs_queue":                       "sqs",
	"aws_dynamodb_table":                  "dynamodb",
	"aws_cloudwatch_log_group":            "cloudwatchlogs",
	"aws_secretsmanager_secret":           "secretsmanager",
	"aws_lambda_function":                 "lambda",
	"aws_iam_role":                        "iam_role",
	"aws_iam_policy":                      "iam_policy",
	"aws_kms_key":                         "kms",
	"aws_s3_bucket":                       "s3",
	"aws_vpc":                             "vpc",
	"aws_subnet":                          "subnet",
	"aws_security_group":                  "security_group",
	"aws_internet_gateway":                "internet_gateway",
	"aws_nat_gateway":                     "nat_gateway",
	"aws_eip":                             "eip",
	"aws_route_table":                     "route_table",
	"aws_network_acl":                     "network_acl",
	"aws_vpc_endpoint":                    "vpc_endpoint",
	"aws_vpc_dhcp_options":                "vpc_dhcp_options",
	"aws_network_interface":               "network_interface",
	"aws_route53_zone":                    "route53_zone",
	"aws_cloudfront_distribution":         "cloudfront_distribution",
	"aws_db_instance":                     "db_instance",
	"aws_db_subnet_group":                 "db_subnet_group",
	"aws_db_parameter_group":              "db_parameter_group",
	"aws_lb":                              "lb",
	"aws_lb_listener":                     "lb_listener",
	"aws_lb_target_group":                 "lb_target_group",
	"aws_bedrock_guardrail":               "bedrock_guardrail",
	"aws_opensearchserverless_collection": "opensearchserverless_collection",
	"aws_apigatewayv2_api":                "apigatewayv2_api",
	"aws_apigatewayv2_stage":              "apigatewayv2_stage",
	"aws_eks_pod_identity_association":    "eks_pod_identity",
	"aws_cloudwatch_event_rule":           "cloudwatch_event_rule",
	"aws_resourceexplorer2_index":         "resourceexplorer2_index",
	"aws_resourceexplorer2_view":          "resourceexplorer2_view",

	// Cloud Control-routed types (Bundle 13). Slug matches the per-type
	// Slug field in cloudControlTypeConfigs (cloudcontrol_types.go);
	// keep both surfaces in sync.
	"aws_backup_vault":             "backup_vault",
	"aws_backup_plan":              "backup_plan",
	"aws_sns_topic":                "sns_topic",
	"aws_cloudwatch_metric_alarm":  "cloudwatch_metric_alarm",
	"aws_cloudwatch_dashboard":     "cloudwatch_dashboard",
}

// ServiceSlug returns the progress-event slug for a Terraform resource
// type, falling back to the type itself when no slug is registered.
// Falling back (rather than panicking) keeps the Emitter safe to call
// from any Discoverer, including test-only ones a future contributor
// might register without updating the slug map.
func ServiceSlug(tfType string) string {
	if s, ok := serviceSlugByTFType[tfType]; ok {
		return s
	}
	return tfType
}

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

// DiscoverTypes runs each named discoverer in series and concatenates the
// results. Unknown type names are reported as a single error containing all
// invalid names (not interleaved with partial results) so the operator sees
// the full set of misspellings in one shot.
//
// The aggregator itself is sequential across resource types — concurrency
// lives inside individual discoverers (DynamoDB, Lambda) where per-item
// tag-fanout dominates wall time. Stage 2c2 (#270) bounded that fanout via
// errgroup; the SDK retryer config in cmd/insideout-import/discover.go
// raises maxAttempts so transient Throttling no longer aborts a run.
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

	stageStart := time.Now()
	var all []imported.ImportedResource
	for _, d := range selected {
		entries, err := d.Discover(ctx, args)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.ResourceType(), err)
		}
		all = append(all, entries...)
	}
	args.Emitter.StageFinish("discover", len(all), time.Since(stageStart))
	return all, nil
}
