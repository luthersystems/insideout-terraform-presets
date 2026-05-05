package awsdiscover

import (
	"context"
	"fmt"
	"path"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// sqsClient is the narrow subset of the SQS SDK the discoverer uses.
// Mirrors the pattern in pkg/observability/discovery/aws (lambdaFunctionsClient
// at compute.go:123) so tests can mock without the full SDK surface.
type sqsClient interface {
	ListQueues(ctx context.Context, in *sqs.ListQueuesInput, opts ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)
}

type sqsDiscoverer struct {
	new func() sqsClient
}

func newSQSDiscoverer(cfg aws.Config) Discoverer {
	return &sqsDiscoverer{new: func() sqsClient { return sqs.NewFromConfig(cfg) }}
}

func (d *sqsDiscoverer) ResourceType() string { return "aws_sqs_queue" }

// Discover lists queues whose names start with the project prefix. SQS
// supports a server-side QueueNamePrefix filter, so we never have to
// download every queue in the account just to filter client-side.
//
// Import ID for aws_sqs_queue is the queue URL itself.
func (d *sqsDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()
	input := &sqs.ListQueuesInput{}
	if project != "" {
		p := project
		input.QueueNamePrefix = &p
	}

	urls, err := paginateListQueues(ctx, client, input)
	if err != nil {
		return nil, fmt.Errorf("ListQueues: %w", err)
	}

	// Sort URLs so the emitted manifest is deterministic across runs.
	sort.Strings(urls)

	book := addressBook{}
	out := make([]imported.ImportedResource, 0, len(urls))
	for _, url := range urls {
		// Queue URL: https://sqs.<region>.amazonaws.com/<account>/<name>
		// The name is the final segment.
		name := path.Base(url)
		out = append(out, makeImportedResource(
			book,
			"aws_sqs_queue",
			name,
			url,
			region,
			accountID,
			map[string]string{"url": url},
		))
	}
	return out, nil
}

// paginateListQueues walks all NextToken pages. The SDK's ListQueues call
// returns at most 1000 queues per page, so this matters on large accounts.
func paginateListQueues(ctx context.Context, client sqsClient, input *sqs.ListQueuesInput) ([]string, error) {
	var all []string
	for {
		out, err := client.ListQueues(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.QueueUrls...)
		if out.NextToken == nil || *out.NextToken == "" {
			return all, nil
		}
		input.NextToken = out.NextToken
	}
}
