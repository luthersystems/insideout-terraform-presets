package discovery

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// lambdaClient defines the Lambda API methods used by the discoverer.
type lambdaClient interface {
	ListFunctions(ctx context.Context, params *lambda.ListFunctionsInput, optFns ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
	ListTags(ctx context.Context, params *lambda.ListTagsInput, optFns ...func(*lambda.Options)) (*lambda.ListTagsOutput, error)
}

// LambdaDiscoverer discovers Lambda functions.
type LambdaDiscoverer struct {
	client lambdaClient
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

			tagsOut, err := d.client.ListTags(ctx, &lambda.ListTagsInput{
				Resource: aws.String(arn),
			})
			if err != nil {
				return nil, fmt.Errorf("lambda list tags for %s: %w", name, err)
			}

			if len(filter.Tags) > 0 && !MatchesTags(tagsOut.Tags, filter.Tags) {
				continue
			}

			resources = append(resources, DiscoveredResource{
				TerraformType: "aws_lambda_function",
				ImportID:      name,
				Name:          name,
				Tags:          tagsOut.Tags,
				ARN:           arn,
			})
		}
	}
	return resources, nil
}
