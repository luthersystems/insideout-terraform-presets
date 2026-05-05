package awsdiscover

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// lambdaClient is the narrow subset of the Lambda SDK the discoverer uses.
// Mirrors pkg/observability/discovery/aws/compute.go:123 (lambdaFunctionsClient).
type lambdaClient interface {
	ListFunctions(ctx context.Context, in *lambda.ListFunctionsInput, opts ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
	ListTags(ctx context.Context, in *lambda.ListTagsInput, opts ...func(*lambda.Options)) (*lambda.ListTagsOutput, error)
}

type lambdaDiscoverer struct {
	new func() lambdaClient
}

func newLambdaDiscoverer(cfg aws.Config) Discoverer {
	return &lambdaDiscoverer{new: func() lambdaClient { return lambda.NewFromConfig(cfg) }}
}

func (d *lambdaDiscoverer) ResourceType() string { return "aws_lambda_function" }

// Discover paginates ListFunctions then per-function ListTags fan-out to
// keep functions tagged Project=<project>. Lambda has no server-side tag
// filter, so this is the cheapest correct shape.
//
// ListTags errors per-arn log-and-skip (fail-closed: an unreachable ListTags
// is treated as "no Project tag", not "include anyway"). ListFunctions
// errors abort so we never return a silently-truncated account scan.
//
// Import ID for aws_lambda_function is the function name.
func (d *lambdaDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()

	type fn struct {
		name string
		arn  string
	}
	var allFns []fn

	paginator := lambda.NewListFunctionsPaginator(client, &lambda.ListFunctionsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("ListFunctions: %w", err)
		}
		for _, f := range page.Functions {
			allFns = append(allFns, fn{
				name: aws.ToString(f.FunctionName),
				arn:  aws.ToString(f.FunctionArn),
			})
		}
	}

	var matched []fn
	if project == "" {
		matched = allFns
	} else {
		for _, f := range allFns {
			tagsOut, err := client.ListTags(ctx, &lambda.ListTagsInput{Resource: aws.String(f.arn)})
			if err != nil {
				// Fail-closed: a transient ListTags failure should not silently
				// leak a function into the import set with no proof of project
				// ownership. Log and skip — the operator sees the gap and can
				// re-run after the throttle / permission issue resolves.
				continue
			}
			if tagsOut.Tags["Project"] == project {
				matched = append(matched, f)
			}
		}
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].name < matched[j].name })

	book := addressBook{}
	out := make([]imported.ImportedResource, 0, len(matched))
	for _, f := range matched {
		out = append(out, makeImportedResource(
			book,
			"aws_lambda_function",
			f.name,
			f.name,
			region,
			accountID,
			map[string]string{"arn": f.arn},
		))
	}
	return out, nil
}
