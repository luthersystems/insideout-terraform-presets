package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	dbSubnetGroupTFType    = "aws_db_subnet_group"
	dbSubnetGroupAssetType = "rds:subgrp"
	dbSubnetGroupSlug      = "db_subnet_group"
)

// dbSubnetGroupClient is the narrow subset of the RDS SDK the
// db_subnet_group discoverer uses. DescribeDBSubnetGroups does NOT
// return tags inline (the response shape has no TagList field on
// DBSubnetGroup) so each candidate row needs a follow-up
// ListTagsForResource fetch keyed by the group's ARN.
type dbSubnetGroupClient interface {
	DescribeDBSubnetGroups(ctx context.Context, in *rds.DescribeDBSubnetGroupsInput, opts ...func(*rds.Options)) (*rds.DescribeDBSubnetGroupsOutput, error)
	ListTagsForResource(ctx context.Context, in *rds.ListTagsForResourceInput, opts ...func(*rds.Options)) (*rds.ListTagsForResourceOutput, error)
}

type dbSubnetGroupDiscoverer struct {
	new            func(region string) dbSubnetGroupClient
	maxConcurrency int
}

func newDBSubnetGroupDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &dbSubnetGroupDiscoverer{
		new: func(region string) dbSubnetGroupClient {
			return rds.NewFromConfig(cfg, func(o *rds.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *dbSubnetGroupDiscoverer) ResourceType() string { return dbSubnetGroupTFType }

// Discover paginates DescribeDBSubnetGroups and filters by
// DBSubnetGroupName-prefix matching args.Project. RDS does not
// expose a server-side filter on DescribeDBSubnetGroups, so the
// prefix check is applied client-side. Per-group tag fetches
// (ListTagsForResource keyed by ARN) fan out under a bounded
// errgroup — same shape as the dynamodb discoverer (#270).
//
// Per-item ListTagsForResource failures stay fail-closed: a transient
// throttle on one group surfaces a stderr WARN and the row is
// dropped, matching dynamodb.go's contract. Parent-context
// cancellation IS propagated.
func (d *dbSubnetGroupDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var imps []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(dbSubnetGroupSlug, region)
		regionCount := 0
		client := d.new(region)

		var groups []rdstypes.DBSubnetGroup
		input := &rds.DescribeDBSubnetGroupsInput{}
		for {
			page, err := client.DescribeDBSubnetGroups(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(dbSubnetGroupSlug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("DescribeDBSubnetGroups (region=%s): %w", region, err)
			}
			groups = append(groups, page.DBSubnetGroups...)
			if page.Marker == nil || *page.Marker == "" {
				break
			}
			input.Marker = page.Marker
		}

		// Filter by project prefix first so we don't fan out
		// ListTagsForResource on every subnet group in the account.
		type entry struct {
			name  string
			arn   string
			vpcID string
			tags  map[string]string
		}
		candidates := make([]entry, 0, len(groups))
		for i := range groups {
			sg := &groups[i]
			name := aws.ToString(sg.DBSubnetGroupName)
			if args.Project != "" && !strings.HasPrefix(name, args.Project) {
				continue
			}
			candidates = append(candidates, entry{
				name:  name,
				arn:   aws.ToString(sg.DBSubnetGroupArn),
				vpcID: aws.ToString(sg.VpcId),
			})
		}

		// Fan out per-group tag fetches under a bounded errgroup.
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		var mu sync.Mutex
		fetched := make([]entry, 0, len(candidates))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, c := range candidates {
			c := c
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				if c.arn == "" {
					// No ARN to key tags on; emit with empty (non-nil) tag
					// map so the nil-vs-empty contract holds for downstream
					// consumers (#291, #289 gap-#6).
					fmt.Fprintf(os.Stderr, "discover: WARN: db_subnet_group %s: empty ARN; emitting with empty tag map (region=%s)\n", c.name, region)
					c.tags = map[string]string{}
					mu.Lock()
					fetched = append(fetched, c)
					mu.Unlock()
					return nil
				}
				tagsOut, err := client.ListTagsForResource(gctx, &rds.ListTagsForResourceInput{ResourceName: aws.String(c.arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: db_subnet_group %s: list tags (region=%s): %v\n", c.name, region, err)
					return nil
				}
				c.tags = rdsTagsToMap(tagsOut.TagList)
				mu.Lock()
				fetched = append(fetched, c)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(dbSubnetGroupSlug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(fetched, func(i, j int) bool { return fetched[i].name < fetched[j].name })

		for _, e := range fetched {
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			native := map[string]string{
				"db_subnet_group_name": e.name,
				"arn":                  e.arn,
				"vpc_id":               e.vpcID,
			}
			imps = append(imps, makeImportedResource(
				book,
				dbSubnetGroupTFType,
				e.name,
				e.name,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(dbSubnetGroupSlug, region, dbSubnetGroupTFType, e.name)
			regionCount++
		}
		args.Emitter.ServiceFinish(dbSubnetGroupSlug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves a DB subnet group by name. Issues a single
// DescribeDBSubnetGroups call to verify existence. Tags are not
// fetched — dep-chase only needs the address/import-ID resolution.
func (d *dbSubnetGroupDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := dbSubnetGroupNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.DescribeDBSubnetGroups(ctx, &rds.DescribeDBSubnetGroupsInput{DBSubnetGroupName: aws.String(name)})
	if err != nil {
		var notFound *rdstypes.DBSubnetGroupNotFoundFault
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_db_subnet_group %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeDBSubnetGroups: %w", err)
	}
	if len(out.DBSubnetGroups) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_db_subnet_group %q: %w", name, ErrNotFound)
	}
	sg := &out.DBSubnetGroups[0]
	native := map[string]string{
		"db_subnet_group_name": name,
		"arn":                  aws.ToString(sg.DBSubnetGroupArn),
		"vpc_id":               aws.ToString(sg.VpcId),
	}
	return makeImportedResource(
		addressBook{},
		dbSubnetGroupTFType,
		name,
		name,
		region,
		accountID,
		native,
		nil,
	), nil
}

// dbSubnetGroupNameFromID validates a DBSubnetGroupName shape.
func dbSubnetGroupNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("db_subnet_group: empty id: %w", ErrNotSupported)
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("db_subnet_group: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
