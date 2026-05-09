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
	dbParameterGroupTFType    = "aws_db_parameter_group"
	dbParameterGroupAssetType = "rds:pg"
	dbParameterGroupSlug      = "db_parameter_group"
)

// dbParameterGroupClient is the narrow subset of the RDS SDK the
// db_parameter_group discoverer uses. DescribeDBParameterGroups does
// NOT return tags inline so each candidate row needs a follow-up
// ListTagsForResource fetch keyed by the group's ARN.
type dbParameterGroupClient interface {
	DescribeDBParameterGroups(ctx context.Context, in *rds.DescribeDBParameterGroupsInput, opts ...func(*rds.Options)) (*rds.DescribeDBParameterGroupsOutput, error)
	ListTagsForResource(ctx context.Context, in *rds.ListTagsForResourceInput, opts ...func(*rds.Options)) (*rds.ListTagsForResourceOutput, error)
}

type dbParameterGroupDiscoverer struct {
	new            func(region string) dbParameterGroupClient
	maxConcurrency int
}

func newDBParameterGroupDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &dbParameterGroupDiscoverer{
		new: func(region string) dbParameterGroupClient {
			return rds.NewFromConfig(cfg, func(o *rds.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *dbParameterGroupDiscoverer) ResourceType() string { return dbParameterGroupTFType }

// Discover paginates DescribeDBParameterGroups and filters by
// DBParameterGroupName-prefix matching args.Project. RDS does not
// expose a server-side filter; the prefix check is applied
// client-side. Per-group tag fetches fan out under a bounded
// errgroup.
//
// Skip-list: groups whose name starts with `default.` are AWS-managed
// (e.g. `default.postgres15`) and cannot be imported — terraform
// import errors with "DBParameterGroupNotFound" or similar. They are
// dropped before tag-fetch fan-out so we never emit them or pay the
// API cost on them.
func (d *dbParameterGroupDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var imps []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(dbParameterGroupSlug, region)
		regionCount := 0
		client := d.new(region)

		var groups []rdstypes.DBParameterGroup
		input := &rds.DescribeDBParameterGroupsInput{}
		for {
			page, err := client.DescribeDBParameterGroups(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(dbParameterGroupSlug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("DescribeDBParameterGroups (region=%s): %w", region, err)
			}
			groups = append(groups, page.DBParameterGroups...)
			if page.Marker == nil || *page.Marker == "" {
				break
			}
			input.Marker = page.Marker
		}

		// Filter by project prefix and skip AWS-managed default.* groups
		// before fan-out so we don't pay ListTagsForResource on tombstone
		// rows.
		type entry struct {
			name   string
			arn    string
			family string
			tags   map[string]string
		}
		candidates := make([]entry, 0, len(groups))
		for i := range groups {
			pg := &groups[i]
			name := aws.ToString(pg.DBParameterGroupName)
			if strings.HasPrefix(name, "default.") {
				continue
			}
			if args.Project != "" && !strings.HasPrefix(name, args.Project) {
				continue
			}
			candidates = append(candidates, entry{
				name:   name,
				arn:    aws.ToString(pg.DBParameterGroupArn),
				family: aws.ToString(pg.DBParameterGroupFamily),
			})
		}

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
					fmt.Fprintf(os.Stderr, "discover: WARN: db_parameter_group %s: list tags (region=%s): %v\n", c.name, region, err)
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
			args.Emitter.ServiceFinish(dbParameterGroupSlug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(fetched, func(i, j int) bool { return fetched[i].name < fetched[j].name })

		for _, e := range fetched {
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			native := map[string]string{
				"db_parameter_group_name": e.name,
				"arn":                     e.arn,
				"family":                  e.family,
			}
			imps = append(imps, makeImportedResource(
				book,
				dbParameterGroupTFType,
				e.name,
				e.name,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(dbParameterGroupSlug, region, dbParameterGroupTFType, e.name)
			regionCount++
		}
		args.Emitter.ServiceFinish(dbParameterGroupSlug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves a DB parameter group by name. Issues a single
// DescribeDBParameterGroups call to verify existence. Tags are not
// fetched — dep-chase only needs the address/import-ID resolution.
func (d *dbParameterGroupDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := dbParameterGroupNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.DescribeDBParameterGroups(ctx, &rds.DescribeDBParameterGroupsInput{DBParameterGroupName: aws.String(name)})
	if err != nil {
		var notFound *rdstypes.DBParameterGroupNotFoundFault
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_db_parameter_group %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeDBParameterGroups: %w", err)
	}
	if len(out.DBParameterGroups) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_db_parameter_group %q: %w", name, ErrNotFound)
	}
	pg := &out.DBParameterGroups[0]
	native := map[string]string{
		"db_parameter_group_name": name,
		"arn":                     aws.ToString(pg.DBParameterGroupArn),
		"family":                  aws.ToString(pg.DBParameterGroupFamily),
	}
	return makeImportedResource(
		addressBook{},
		dbParameterGroupTFType,
		name,
		name,
		region,
		accountID,
		native,
		nil,
	), nil
}

// dbParameterGroupNameFromID validates a DBParameterGroupName shape.
func dbParameterGroupNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("db_parameter_group: empty id: %w", ErrNotSupported)
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("db_parameter_group: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
