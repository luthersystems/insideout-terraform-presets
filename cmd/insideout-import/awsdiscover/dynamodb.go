package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// dynamoClient is the narrow subset of the DynamoDB SDK we consume.
type dynamoClient interface {
	ListTables(ctx context.Context, in *dynamodb.ListTablesInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error)
	ListTagsOfResource(ctx context.Context, in *dynamodb.ListTagsOfResourceInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error)
	DescribeTable(ctx context.Context, in *dynamodb.DescribeTableInput, opts ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
}

type dynamoDiscoverer struct {
	new            func(region string) dynamoClient
	maxConcurrency int
}

func newDynamoDBDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &dynamoDiscoverer{
		new: func(region string) dynamoClient {
			return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *dynamoDiscoverer) ResourceType() string { return "aws_dynamodb_table" }

// Discover paginates ListTables and filters by name prefix (DynamoDB does
// not expose server-side filters on ListTables) — then for each candidate
// runs ListTagsOfResource to fetch the table's tag map. The fetched map
// is matched against args.TagSelectors and persisted onto Identity.Tags.
//
// Per-table tag lookups fan out under a bounded errgroup so a few-thousand
// table account does not serialize into a multi-minute wall-time. Per-item
// SDK errors stay fail-closed (transient ListTagsOfResource failures skip
// the table rather than aborting the run, since the SDK retryer has
// already exhausted its budget by then). Parent-context cancellation IS
// propagated: gctx unblocks any in-flight goroutines and Discover returns
// ctx.Err() rather than a silently-truncated set.
//
// Multi-region (#291): outer loop walks args.Regions building a per-region
// SDK client. The legacy "Project=<project>" tag check is preserved as a
// back-compat implicit filter when args.Project is non-empty — operators
// that scan composer-emitted stacks rely on this dual (name-prefix +
// Project-tag) defense. Operator-supplied selectors are AND'd on top.
//
// Import ID for aws_dynamodb_table is the table name.
func (d *dynamoDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "dynamodb"
	var imps []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		var all []string
		input := &dynamodb.ListTablesInput{}
		for {
			out, err := client.ListTables(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListTables (region=%s): %w", region, err)
			}
			for _, t := range out.TableNames {
				if args.Project == "" || hasPrefix(t, args.Project) {
					all = append(all, t)
				}
			}
			if out.LastEvaluatedTableName == nil || *out.LastEvaluatedTableName == "" {
				break
			}
			input.ExclusiveStartTableName = out.LastEvaluatedTableName
		}

		// Per-table tag map fetched once, reused for selector match + persistence.
		type tableEntry struct {
			name string
			tags map[string]string
			arn  string
		}
		entries := make([]tableEntry, 0, len(all))
		canFetchTags := args.AccountID != "" && region != ""
		if !canFetchTags {
			// Without an account ID + region we cannot construct the ARN
			// ListTagsOfResource needs. Fall back to prefix-only filtering and
			// trust the operator-supplied scope; tags stay nil to signal
			// "didn't fetch."
			for _, name := range all {
				entries = append(entries, tableEntry{name: name})
			}
		} else {
			var mu sync.Mutex
			ok := make([]tableEntry, 0, len(all))
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
					arn := fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, args.AccountID, name)
					tagsOut, err := client.ListTagsOfResource(gctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
					if err != nil {
						if cerr := gctx.Err(); cerr != nil {
							return cerr
						}
						return nil
					}
					tags := make(map[string]string, len(tagsOut.Tags))
					for _, tag := range tagsOut.Tags {
						tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
					}
					mu.Lock()
					ok = append(ok, tableEntry{name: name, tags: tags, arn: arn})
					mu.Unlock()
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListTagsOfResource (region=%s): %w", region, err)
			}
			entries = ok
		}

		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

		for _, e := range entries {
			// Legacy back-compat: when project is non-empty AND we
			// successfully fetched tags, require the Project=<project>
			// tag. Composer-emitted stacks rely on this dual-check; we
			// skip it on the prefix-only fallback path (canFetchTags
			// false) to preserve that path's existing behavior. Tags
			// nil ⇒ "didn't fetch" ⇒ skip the Project check.
			if canFetchTags && args.Project != "" && e.tags != nil && e.tags["Project"] != args.Project {
				continue
			}
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			arn := e.arn
			if arn == "" && args.AccountID != "" && region != "" {
				arn = fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, args.AccountID, e.name)
			}
			imps = append(imps, makeImportedResource(
				book,
				"aws_dynamodb_table",
				e.name,
				e.name,
				region,
				args.AccountID,
				map[string]string{"arn": arn},
				e.tags,
			))
			args.Emitter.ItemFound(slug, region, "aws_dynamodb_table", e.name)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// hasPrefix is a stdlib helper inlined here so the prefix check stays
// readable next to the ListTables loop.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// DiscoverByID resolves a DynamoDB table by ARN
// (arn:aws:dynamodb:<region>:<account>:table/<name>) or bare table
// name. Issues a single DescribeTable call to verify existence.
func (d *dynamoDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := dynamoNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)})
	if err != nil {
		var notFound *dynamotypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_dynamodb_table %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeTable: %w", err)
	}
	arn := ""
	if out.Table != nil {
		arn = aws.ToString(out.Table.TableArn)
	}
	if arn == "" && accountID != "" && region != "" {
		arn = fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, accountID, name)
	}
	// DiscoverByID does not fetch tags — dep-chase only needs the
	// resource's address/import-ID resolution, not its tag map.
	return makeImportedResource(
		addressBook{},
		"aws_dynamodb_table",
		name,
		name,
		region,
		accountID,
		map[string]string{"arn": arn},
		nil,
	), nil
}

// dynamoNameFromID extracts the DynamoDB table name from an ARN
// (arn:aws:dynamodb:<region>:<account>:table/<name>) or bare name.
func dynamoNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("dynamodb: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("dynamodb: parse arn: %w", err)
		}
		if parsed.Service != "dynamodb" {
			return "", fmt.Errorf("dynamodb: not a dynamodb arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// Resource is "table/<name>" — split on first slash.
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "table" || parts[1] == "" {
			return "", fmt.Errorf("dynamodb: arn resource %q is not table/<name>: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("dynamodb: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
