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

	"github.com/aws/aws-sdk-go-v2/aws"

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
	return &AWSDiscoverer{
		defaultRegion: cfg.Region,
		byType: map[string]Discoverer{
			"aws_sqs_queue":             newSQSDiscoverer(cfg),
			"aws_dynamodb_table":        newDynamoDBDiscoverer(cfg, maxConcurrency),
			"aws_cloudwatch_log_group":  newCloudWatchLogsDiscoverer(cfg),
			"aws_secretsmanager_secret": newSecretsManagerDiscoverer(cfg),
			"aws_lambda_function":       newLambdaDiscoverer(cfg, maxConcurrency),
			"aws_iam_role":              newIAMRoleDiscoverer(cfg),
			"aws_iam_policy":            newIAMPolicyDiscoverer(cfg),
			"aws_kms_key":               newKMSDiscoverer(cfg),
			"aws_s3_bucket":             newS3Discoverer(cfg),
		},
	}
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

	var all []imported.ImportedResource
	for _, d := range selected {
		entries, err := d.Discover(ctx, args)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.ResourceType(), err)
		}
		all = append(all, entries...)
	}
	return all, nil
}
