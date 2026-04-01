package discovery

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// LambdaDiscoverer discovers Lambda functions.
type LambdaDiscoverer struct {
	client *lambda.Client
}

func NewLambdaDiscoverer(cfg aws.Config) *LambdaDiscoverer {
	return &LambdaDiscoverer{client: lambda.NewFromConfig(cfg)}
}

func (d *LambdaDiscoverer) ResourceType() string { return "aws_lambda_function" }

func (d *LambdaDiscoverer) Discover(ctx context.Context, filter Filter) ([]DiscoveredResource, error) {
	var resources []DiscoveredResource

	// Lambda ListFunctions does not support name prefix filtering,
	// so we paginate through all and filter client-side.
	paginator := lambda.NewListFunctionsPaginator(d.client, &lambda.ListFunctionsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("lambda list functions: %w", err)
		}
		for _, fn := range page.Functions {
			name := aws.ToString(fn.FunctionName)
			if filter.Project != "" && !MatchesPrefix(name, filter.Project) {
				continue
			}

			arn := aws.ToString(fn.FunctionArn)

			tags, err := d.getFunctionTags(ctx, arn)
			if err != nil {
				return nil, fmt.Errorf("lambda list tags for %s: %w", name, err)
			}

			if len(filter.Tags) > 0 && !MatchesTags(tags, filter.Tags) {
				continue
			}

			resources = append(resources, DiscoveredResource{
				TerraformType: "aws_lambda_function",
				ImportID:      name,
				Name:          name,
				Tags:          tags,
				ARN:           arn,
			})
		}
	}
	return resources, nil
}

func (d *LambdaDiscoverer) getFunctionTags(ctx context.Context, arn string) (map[string]string, error) {
	out, err := d.client.ListTags(ctx, &lambda.ListTagsInput{
		Resource: aws.String(arn),
	})
	if err != nil {
		return nil, err
	}
	return out.Tags, nil
}
