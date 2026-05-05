package awsdiscover

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// dynamoClient is the narrow subset of the DynamoDB SDK we consume.
type dynamoClient interface {
	ListTables(ctx context.Context, in *dynamodb.ListTablesInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error)
	ListTagsOfResource(ctx context.Context, in *dynamodb.ListTagsOfResourceInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error)
}

type dynamoDiscoverer struct {
	new            func() dynamoClient
	maxConcurrency int
}

func newDynamoDBDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &dynamoDiscoverer{
		new:            func() dynamoClient { return dynamodb.NewFromConfig(cfg) },
		maxConcurrency: maxConcurrency,
	}
}

func (d *dynamoDiscoverer) ResourceType() string { return "aws_dynamodb_table" }

// Discover paginates ListTables and filters by name prefix (DynamoDB does
// not expose server-side filters on ListTables) — then for each candidate
// runs ListTagsOfResource to confirm the Project tag. The double check
// (prefix + tag) defends against table names that share the project prefix
// by accident.
//
// Per-table tag lookups fan out under a bounded errgroup so a few-thousand
// table account does not serialize into a multi-minute wall-time. Per-item
// SDK errors stay fail-closed (transient ListTagsOfResource failures skip
// the table rather than aborting the run, since the SDK retryer has
// already exhausted its budget by then). Parent-context cancellation IS
// propagated: gctx unblocks any in-flight goroutines and Discover returns
// ctx.Err() rather than a silently-truncated set.
//
// Import ID for aws_dynamodb_table is the table name.
func (d *dynamoDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()

	var all []string
	input := &dynamodb.ListTablesInput{}
	for {
		out, err := client.ListTables(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("ListTables: %w", err)
		}
		for _, t := range out.TableNames {
			if project == "" || hasPrefix(t, project) {
				all = append(all, t)
			}
		}
		if out.LastEvaluatedTableName == nil || *out.LastEvaluatedTableName == "" {
			break
		}
		input.ExclusiveStartTableName = out.LastEvaluatedTableName
	}

	var matched []string
	if project == "" || accountID == "" || region == "" {
		// Without an account ID + region we cannot construct the ARN
		// ListTagsOfResource needs. Fall back to prefix-only filtering and
		// trust the operator-supplied scope.
		matched = all
	} else {
		var (
			mu sync.Mutex
			ok []string
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, name := range all {
			name := name
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				arn := fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, accountID, name)
				tagsOut, err := client.ListTagsOfResource(gctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					return nil
				}
				for _, tag := range tagsOut.Tags {
					if aws.ToString(tag.Key) == "Project" && aws.ToString(tag.Value) == project {
						mu.Lock()
						ok = append(ok, name)
						mu.Unlock()
						return nil
					}
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, fmt.Errorf("ListTagsOfResource: %w", err)
		}
		matched = ok
	}

	sort.Strings(matched)

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(matched))
	for _, name := range matched {
		var arn string
		if accountID != "" && region != "" {
			arn = fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, accountID, name)
		}
		imps = append(imps, makeImportedResource(
			book,
			"aws_dynamodb_table",
			name,
			name,
			region,
			accountID,
			map[string]string{"arn": arn},
		))
	}
	return imps, nil
}

// hasPrefix is a stdlib helper inlined here so the prefix check stays
// readable next to the ListTables loop. Using strings.HasPrefix would
// require importing "strings" only for one call.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
