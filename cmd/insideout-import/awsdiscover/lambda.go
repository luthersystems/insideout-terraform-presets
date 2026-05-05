package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// lambdaClient is the narrow subset of the Lambda SDK the discoverer uses.
// Mirrors pkg/observability/discovery/aws/compute.go:123 (lambdaFunctionsClient).
type lambdaClient interface {
	ListFunctions(ctx context.Context, in *lambda.ListFunctionsInput, opts ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
	ListTags(ctx context.Context, in *lambda.ListTagsInput, opts ...func(*lambda.Options)) (*lambda.ListTagsOutput, error)
	GetFunction(ctx context.Context, in *lambda.GetFunctionInput, opts ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error)
}

type lambdaDiscoverer struct {
	new            func() lambdaClient
	maxConcurrency int
}

func newLambdaDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &lambdaDiscoverer{
		new:            func() lambdaClient { return lambda.NewFromConfig(cfg) },
		maxConcurrency: maxConcurrency,
	}
}

func (d *lambdaDiscoverer) ResourceType() string { return "aws_lambda_function" }

// Discover paginates ListFunctions then per-function ListTags fan-out to
// keep functions tagged Project=<project>. Lambda has no server-side tag
// filter, so this is the cheapest correct shape.
//
// Per-function ListTags calls run under a bounded errgroup so a thousand-
// function account does not serialize into a multi-minute wall-time.
// Per-item SDK errors are fail-closed (an unreachable ListTags is treated
// as "no Project tag", not "include anyway") since the SDK retryer has
// already exhausted its budget. Parent-context cancellation IS propagated:
// gctx unblocks any in-flight goroutines and Discover returns ctx.Err()
// rather than a silently-truncated set. ListFunctions errors abort.
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
		var (
			mu sync.Mutex
			ok []fn
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, f := range allFns {
			f := f
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				tagsOut, err := client.ListTags(gctx, &lambda.ListTagsInput{Resource: aws.String(f.arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					return nil
				}
				if tagsOut.Tags["Project"] == project {
					mu.Lock()
					ok = append(ok, f)
					mu.Unlock()
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, fmt.Errorf("ListTags: %w", err)
		}
		matched = ok
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

// DiscoverByID resolves a Lambda function by ARN
// (arn:aws:lambda:<region>:<account>:function:<name>) or bare function
// name. Issues a single GetFunction call to verify existence.
func (d *lambdaDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := lambdaNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new()
	out, err := client.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(name)})
	if err != nil {
		var notFound *lambdatypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_lambda_function %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetFunction: %w", err)
	}
	arn := ""
	if out.Configuration != nil {
		arn = aws.ToString(out.Configuration.FunctionArn)
	}
	return makeImportedResource(
		addressBook{},
		"aws_lambda_function",
		name,
		name,
		region,
		accountID,
		map[string]string{"arn": arn},
	), nil
}

// lambdaNameFromID extracts the function name from an ARN
// (arn:aws:lambda:<region>:<account>:function:<name>[:<version-or-alias>])
// or bare name. The function ARN's resource portion uses ":" not "/" as
// the delimiter, and may carry a version/alias suffix that we strip.
func lambdaNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("lambda: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("lambda: parse arn: %w", err)
		}
		if parsed.Service != "lambda" {
			return "", fmt.Errorf("lambda: not a lambda arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// function:<name>[:<qualifier>]
		const prefix = "function:"
		if !strings.HasPrefix(parsed.Resource, prefix) {
			return "", fmt.Errorf("lambda: arn resource %q is not function:<name>: %w", parsed.Resource, ErrNotSupported)
		}
		rest := strings.TrimPrefix(parsed.Resource, prefix)
		// Drop a trailing :version or :alias if present.
		if i := strings.IndexByte(rest, ':'); i != -1 {
			rest = rest[:i]
		}
		if rest == "" {
			return "", fmt.Errorf("lambda: empty name in arn %q: %w", id, ErrNotSupported)
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("lambda: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
