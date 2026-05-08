package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// sqsClient is the narrow subset of the SQS SDK the discoverer uses.
// Mirrors the pattern in pkg/observability/discovery/aws (lambdaFunctionsClient
// at compute.go:123) so tests can mock without the full SDK surface.
type sqsClient interface {
	ListQueues(ctx context.Context, in *sqs.ListQueuesInput, opts ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)
	GetQueueUrl(ctx context.Context, in *sqs.GetQueueUrlInput, opts ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
	ListQueueTags(ctx context.Context, in *sqs.ListQueueTagsInput, opts ...func(*sqs.Options)) (*sqs.ListQueueTagsOutput, error)
}

type sqsDiscoverer struct {
	new func(region string) sqsClient
}

func newSQSDiscoverer(cfg aws.Config) Discoverer {
	return &sqsDiscoverer{new: func(region string) sqsClient {
		return sqs.NewFromConfig(cfg, func(o *sqs.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *sqsDiscoverer) ResourceType() string { return "aws_sqs_queue" }

// Discover lists queues whose names start with the project prefix. SQS
// supports a server-side QueueNamePrefix filter, so we never have to
// download every queue in the account just to filter client-side.
//
// Multi-region (#291): loops args.Regions, building a per-region SDK
// client. Per-queue ListQueueTags fetches the tag map for tag-selector
// post-filtering and tag persistence onto Identity.Tags.
//
// Import ID for aws_sqs_queue is the queue URL itself.
func (d *sqsDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	book := addressBook{}
	var out []imported.ImportedResource
	for _, region := range args.Regions {
		client := d.new(region)
		input := &sqs.ListQueuesInput{}
		if args.Project != "" {
			p := args.Project
			input.QueueNamePrefix = &p
		}

		urls, err := paginateListQueues(ctx, client, input)
		if err != nil {
			return nil, fmt.Errorf("ListQueues (region=%s): %w", region, err)
		}

		// Sort URLs so the emitted manifest is deterministic across runs.
		sort.Strings(urls)

		for _, url := range urls {
			// Queue URL: https://sqs.<region>.amazonaws.com/<account>/<name>
			// The name is the final segment.
			name := path.Base(url)
			tags, err := fetchSQSTags(ctx, client, url)
			if err != nil {
				return nil, fmt.Errorf("ListQueueTags (region=%s, queue=%s): %w", region, name, err)
			}
			if !MatchesAll(tags, args.TagSelectors) {
				continue
			}
			out = append(out, makeImportedResource(
				book,
				"aws_sqs_queue",
				name,
				url,
				region,
				args.AccountID,
				map[string]string{"url": url},
				tags,
			))
		}
	}
	return out, nil
}

// fetchSQSTags returns the queue's tag map, normalizing a nil-result
// from the SDK into an empty (non-nil) map so the filter+persist
// contract is preserved (nil ⇒ "didn't fetch", empty ⇒ "no tags").
func fetchSQSTags(ctx context.Context, client sqsClient, queueURL string) (map[string]string, error) {
	out, err := client.ListQueueTags(ctx, &sqs.ListQueueTagsInput{QueueUrl: aws.String(queueURL)})
	if err != nil {
		return nil, err
	}
	if out.Tags == nil {
		return map[string]string{}, nil
	}
	return out.Tags, nil
}

// DiscoverByID resolves an SQS queue from a queue URL or ARN. The
// canonical Terraform import ID for aws_sqs_queue is the queue URL —
// generated.tf almost always references queues by ARN, so we accept
// either shape and call GetQueueUrl by name to verify the queue exists.
func (d *sqsDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := sqsNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}

	client := d.new(region)
	out, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		var notFound *sqstypes.QueueDoesNotExist
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_sqs_queue %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetQueueUrl: %w", err)
	}
	url := aws.ToString(out.QueueUrl)

	tags, err := fetchSQSTags(ctx, client, url)
	if err != nil {
		return imported.ImportedResource{}, fmt.Errorf("ListQueueTags (queue=%s): %w", name, err)
	}
	return makeImportedResource(
		addressBook{},
		"aws_sqs_queue",
		name,
		url,
		region,
		accountID,
		map[string]string{"url": url},
		tags,
	), nil
}

// sqsNameFromID extracts the queue name from one of three accepted inputs:
// a queue URL (https://sqs.<region>.amazonaws.com/<account>/<name>), an
// SQS ARN (arn:aws:sqs:<region>:<account>:<name>), or the bare queue
// name. Anything else returns ErrNotSupported so dep-chase can route it
// to its unresolvable-warning bucket.
func sqsNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("sqs: empty id: %w", ErrNotSupported)
	}
	if strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "http://") {
		return path.Base(id), nil
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("sqs: parse arn: %w", err)
		}
		if parsed.Service != "sqs" {
			return "", fmt.Errorf("sqs: not an sqs arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// SQS ARN's Resource is the bare queue name (no resource-type prefix).
		return parsed.Resource, nil
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("sqs: unrecognized id %q: %w", id, ErrNotSupported)
	}
	// Treat as a bare queue name.
	return id, nil
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
