package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// SQSDiscoverer discovers SQS queues.
type SQSDiscoverer struct {
	client *sqs.Client
}

func NewSQSDiscoverer(cfg aws.Config) *SQSDiscoverer {
	return &SQSDiscoverer{client: sqs.NewFromConfig(cfg)}
}

func (d *SQSDiscoverer) ResourceType() string { return "aws_sqs_queue" }

func (d *SQSDiscoverer) Discover(ctx context.Context, filter Filter) ([]DiscoveredResource, error) {
	input := &sqs.ListQueuesInput{}
	if filter.Project != "" {
		input.QueueNamePrefix = aws.String(filter.Project)
	}

	var resources []DiscoveredResource
	paginator := sqs.NewListQueuesPaginator(d.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("sqs list queues: %w", err)
		}
		for _, queueURL := range page.QueueUrls {
			name := queueNameFromURL(queueURL)

			tags, err := d.getQueueTags(ctx, queueURL)
			if err != nil {
				return nil, fmt.Errorf("sqs get tags for %s: %w", name, err)
			}

			if len(filter.Tags) > 0 && !MatchesTags(tags, filter.Tags) {
				continue
			}

			arn, err := d.getQueueARN(ctx, queueURL)
			if err != nil {
				return nil, fmt.Errorf("sqs get arn for %s: %w", name, err)
			}

			resources = append(resources, DiscoveredResource{
				TerraformType: "aws_sqs_queue",
				ImportID:      queueURL,
				Name:          name,
				Tags:          tags,
				ARN:           arn,
			})
		}
	}
	return resources, nil
}

func (d *SQSDiscoverer) getQueueTags(ctx context.Context, queueURL string) (map[string]string, error) {
	out, err := d.client.ListQueueTags(ctx, &sqs.ListQueueTagsInput{
		QueueUrl: aws.String(queueURL),
	})
	if err != nil {
		return nil, err
	}
	return out.Tags, nil
}

func (d *SQSDiscoverer) getQueueARN(ctx context.Context, queueURL string) (string, error) {
	out, err := d.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return "", err
	}
	return out.Attributes[string(sqstypes.QueueAttributeNameQueueArn)], nil
}

// queueNameFromURL extracts the queue name from a queue URL.
// URL format: https://sqs.<region>.amazonaws.com/<account>/<queue-name>
func queueNameFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return url
}
