// Package awsdiscover holds the AWS-side per-resource-type discoverers used
// by the insideout-import discover subcommand (Stage 2a of #189). Each
// discoverer issues read-only AWS SDK calls against the operator's account
// and returns Phase 2 imported.ImportedResource entries — no terraform-exec,
// no HCL generation. Stage 2b layers `terraform plan -generate-config-out`
// on top of this manifest to produce the actual .tf files.
//
// Discoverers in this package own narrow client interfaces so unit tests
// can mock the SDK boundary without depending on real AWS credentials.
// The aggregator (AWSDiscoverer) wires real SDK clients in production and
// fans out to the registered per-type discoverers.
package awsdiscover

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Discoverer is the per-resource-type contract. Each implementation handles
// one Terraform type (e.g. "aws_sqs_queue") and returns []imported.ImportedResource
// directly — no intermediate flat shape, per #189.
//
// Project, region, and accountID are passed in rather than re-derived per
// discoverer so the aggregator can call STS GetCallerIdentity once.
type Discoverer interface {
	// ResourceType returns the Terraform type this discoverer covers, e.g.
	// "aws_sqs_queue".
	ResourceType() string
	// Discover performs read-only SDK calls and returns one ImportedResource
	// per matched cloud resource. Implementations populate Identity and set
	// Tier=TierImportedFlat, Source=SourceImporter on each entry.
	Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error)
}

// AWSDiscoverer aggregates the per-type discoverers and fans out a single
// DiscoverTypes call across all of them. Construct with NewAWSDiscoverer
// in production; tests can build it directly with mock discoverers.
type AWSDiscoverer struct {
	byType map[string]Discoverer
}

// NewAWSDiscoverer wires up the production set of AWS discoverers (the 5
// Phase 1 types: SQS, DynamoDB, CloudWatch Logs, Secrets Manager, Lambda).
// All discoverers share the same aws.Config; per-type SDK clients are
// constructed inside each discoverer.
func NewAWSDiscoverer(cfg aws.Config) *AWSDiscoverer {
	return &AWSDiscoverer{
		byType: map[string]Discoverer{
			"aws_sqs_queue":             newSQSDiscoverer(cfg),
			"aws_dynamodb_table":        newDynamoDBDiscoverer(cfg),
			"aws_cloudwatch_log_group":  newCloudWatchLogsDiscoverer(cfg),
			"aws_secretsmanager_secret": newSecretsManagerDiscoverer(cfg),
			"aws_lambda_function":       newLambdaDiscoverer(cfg),
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

// DiscoverTypes runs each named discoverer in series and concatenates the
// results. Unknown type names are reported as a single error containing all
// invalid names (not interleaved with partial results) so the operator sees
// the full set of misspellings in one shot.
//
// Concurrency is intentionally serial in Stage 2a — Stage 2c will introduce
// errgroup with bounded parallelism + the SDK retryer config (per #189's
// QA carry-forward). Keeping it simple here means deterministic test output
// and no throttling surprises on small/medium accounts.
func (a *AWSDiscoverer) DiscoverTypes(ctx context.Context, types []string, project, region, accountID string) ([]imported.ImportedResource, error) {
	if len(types) == 0 {
		types = a.SupportedTypes()
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
		entries, err := d.Discover(ctx, project, region, accountID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.ResourceType(), err)
		}
		all = append(all, entries...)
	}
	return all, nil
}
