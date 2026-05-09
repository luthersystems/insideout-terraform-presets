// Package awsdiscover — Resource Explorer 2 view discoverer.
//
// Resource Explorer 2 views (like indexes) are an account-level setup
// primitive. Customers typically create one or two views per account
// to filter the inventory the search API returns. Views are not
// project-tagged in the InsideOut sense, so this discoverer applies
// NO project / name / prefix filter — every view in every requested
// region is returned. Operator-supplied TagSelectors still apply
// post-fetch as a normal AND-conjunction filter.
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
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	re2types "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	re2ViewTFType    = "aws_resourceexplorer2_view"
	re2ViewAssetType = "resource-explorer-2:view"
)

// resourceExplorer2ViewClient is the narrow subset of the Resource
// Explorer 2 SDK the view discoverer uses. Mirrors the per-service
// interface pattern used everywhere else in this package so tests can
// mock the SDK boundary without depending on real AWS credentials.
type resourceExplorer2ViewClient interface {
	ListViews(ctx context.Context, in *resourceexplorer2.ListViewsInput, opts ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListViewsOutput, error)
	GetView(ctx context.Context, in *resourceexplorer2.GetViewInput, opts ...func(*resourceexplorer2.Options)) (*resourceexplorer2.GetViewOutput, error)
	ListTagsForResource(ctx context.Context, in *resourceexplorer2.ListTagsForResourceInput, opts ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListTagsForResourceOutput, error)
}

type resourceExplorer2ViewDiscoverer struct {
	new            func(region string) resourceExplorer2ViewClient
	maxConcurrency int
}

func newResourceExplorer2ViewDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &resourceExplorer2ViewDiscoverer{
		new: func(region string) resourceExplorer2ViewClient {
			return resourceexplorer2.NewFromConfig(cfg, func(o *resourceexplorer2.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *resourceExplorer2ViewDiscoverer) ResourceType() string { return re2ViewTFType }

// Discover paginates ListViews per region and returns every view's
// ARN. NO project / prefix filter is applied (see file header).
// Operator TagSelectors still apply post-fetch.
//
// Per-view ListTagsForResource fetches the tag map under a bounded
// errgroup. Per-item failures are fail-closed (transient errors skip
// the view with a stderr WARN); ListViews errors abort the region.
//
// Import ID for aws_resourceexplorer2_view is the view ARN as-is.
func (d *resourceExplorer2ViewDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	const slug = "resourceexplorer2_view"
	book := addressBook{}
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		type view struct {
			arn  string
			tags map[string]string
		}
		var allViews []view

		input := &resourceexplorer2.ListViewsInput{}
		for {
			out, err := client.ListViews(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListViews (region=%s): %w", region, err)
			}
			for _, v := range out.Views {
				allViews = append(allViews, view{arn: v})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		// Per-view tag fetch under bounded errgroup.
		var (
			mu sync.Mutex
			ok []view
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, v := range allViews {
			v := v
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				if v.arn == "" {
					mu.Lock()
					ok = append(ok, v)
					mu.Unlock()
					return nil
				}
				tagsOut, err := client.ListTagsForResource(gctx, &resourceexplorer2.ListTagsForResourceInput{ResourceArn: aws.String(v.arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: resourceexplorer2_view %s: list tags (region=%s): %v\n", v.arn, region, err)
					return nil
				}
				tags := tagsOut.Tags
				if tags == nil {
					tags = map[string]string{}
				}
				v.tags = tags
				mu.Lock()
				ok = append(ok, v)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool { return ok[i].arn < ok[j].arn })

		for _, v := range ok {
			if !MatchesAll(v.tags, args.TagSelectors) {
				continue
			}
			parsedRegion, parsedName := re2ViewArnRegionAndName(v.arn)
			// Prefer the human-readable view name parsed from the ARN
			// (arn:.../view/<name>/<uuid>) over the UUID-suffix
			// re2NameFromArn returns. The UUID is unstable across
			// recreate cycles and useless for the address picker.
			name := parsedName
			if name == "" {
				name = re2NameFromArn(v.arn)
			}
			native := map[string]string{
				"arn": v.arn,
			}
			if parsedRegion != "" {
				native["region"] = parsedRegion
			}
			if parsedName != "" {
				native["name"] = parsedName
			}
			imps = append(imps, makeImportedResource(
				book,
				re2ViewTFType,
				name,
				v.arn,
				region,
				args.AccountID,
				native,
				v.tags,
			))
			args.Emitter.ItemFound(slug, region, re2ViewTFType, v.arn)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves a Resource Explorer 2 view by its ARN. Issues
// a single GetView call to verify existence.
func (d *resourceExplorer2ViewDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	if err := validateResourceExplorer2ARN(id); err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.GetView(ctx, &resourceexplorer2.GetViewInput{ViewArn: aws.String(id)})
	if err != nil {
		var notFound *re2types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_resourceexplorer2_view %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetView: %w", err)
	}
	if out.View == nil {
		return imported.ImportedResource{}, fmt.Errorf("aws_resourceexplorer2_view %q: %w", id, ErrNotFound)
	}
	arn := aws.ToString(out.View.ViewArn)
	if arn == "" {
		arn = id
	}
	parsedRegion, parsedName := re2ViewArnRegionAndName(arn)
	name := parsedName
	if name == "" {
		name = re2NameFromArn(arn)
	}
	native := map[string]string{"arn": arn}
	if parsedRegion != "" {
		native["region"] = parsedRegion
	}
	if parsedName != "" {
		native["name"] = parsedName
	}
	return makeImportedResource(
		addressBook{},
		re2ViewTFType,
		name,
		arn,
		region,
		accountID,
		native,
		nil,
	), nil
}

// re2ViewArnRegionAndName parses a Resource Explorer 2 view ARN of the
// shape arn:aws:resource-explorer-2:<region>:<account>:view/<name>/<id>
// and returns (region, name). Returns ("", "") on any shape that does
// not match.
func re2ViewArnRegionAndName(arn string) (string, string) {
	// arn:aws:resource-explorer-2:us-east-1:123:view/foo/uuid
	if !strings.HasPrefix(arn, "arn:") {
		return "", ""
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 {
		return "", ""
	}
	region := parts[3]
	resource := parts[5] // "view/<name>/<id>"
	if !strings.HasPrefix(resource, "view/") {
		return region, ""
	}
	rest := strings.TrimPrefix(resource, "view/")
	// rest is "<name>/<id>"; the name is everything before the last slash.
	if i := strings.LastIndex(rest, "/"); i > 0 {
		return region, rest[:i]
	}
	return region, ""
}
