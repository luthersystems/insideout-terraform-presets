// Package awsdiscover — Resource Explorer 2 index discoverer.
//
// Resource Explorer 2 indexes are an account-level setup primitive: at
// most one index per region per account, and they are not project-tagged
// in the InsideOut sense (the customer enables Resource Explorer once
// for the whole account so subsequent --include-unsupported scans can
// search the inventory). Because of that, this discoverer applies NO
// project / name / prefix filter — every index in every requested
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
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	re2types "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	re2IndexTFType    = "aws_resourceexplorer2_index"
	re2IndexAssetType = "resource-explorer-2:index"
)

// resourceExplorer2IndexClient is the narrow subset of the Resource
// Explorer 2 SDK the index discoverer uses. Mirrors the per-service
// interface pattern used everywhere else in this package so tests can
// mock the SDK boundary without depending on real AWS credentials.
type resourceExplorer2IndexClient interface {
	ListIndexes(ctx context.Context, in *resourceexplorer2.ListIndexesInput, opts ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListIndexesOutput, error)
	GetIndex(ctx context.Context, in *resourceexplorer2.GetIndexInput, opts ...func(*resourceexplorer2.Options)) (*resourceexplorer2.GetIndexOutput, error)
	ListTagsForResource(ctx context.Context, in *resourceexplorer2.ListTagsForResourceInput, opts ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListTagsForResourceOutput, error)
}

type resourceExplorer2IndexDiscoverer struct {
	new            func(region string) resourceExplorer2IndexClient
	maxConcurrency int
}

func newResourceExplorer2IndexDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &resourceExplorer2IndexDiscoverer{
		new: func(region string) resourceExplorer2IndexClient {
			return resourceexplorer2.NewFromConfig(cfg, func(o *resourceexplorer2.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *resourceExplorer2IndexDiscoverer) ResourceType() string { return re2IndexTFType }

// Discover paginates ListIndexes per region and returns every index
// found. NO project / prefix filter is applied (see the file header
// for the rationale). Operator TagSelectors still apply post-fetch.
//
// Per-index ListTagsForResource fetches the tag map under a bounded
// errgroup. Per-item failures are fail-closed (transient errors skip
// the index with a stderr WARN); ListIndexes errors abort the region.
//
// Import ID for aws_resourceexplorer2_index is the index ARN.
func (d *resourceExplorer2IndexDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	const slug = "resourceexplorer2_index"
	book := addressBook{}
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		type idx struct {
			arn       string
			indexType string
			region    string
			tags      map[string]string
		}
		var allIxs []idx

		input := &resourceexplorer2.ListIndexesInput{}
		for {
			out, err := client.ListIndexes(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListIndexes (region=%s): %w", region, err)
			}
			for _, ix := range out.Indexes {
				allIxs = append(allIxs, idx{
					arn:       aws.ToString(ix.Arn),
					indexType: string(ix.Type),
					region:    aws.ToString(ix.Region),
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		// Per-index tag fetch under bounded errgroup.
		var (
			mu sync.Mutex
			ok []idx
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, ix := range allIxs {
			ix := ix
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				if ix.arn == "" {
					mu.Lock()
					ok = append(ok, ix)
					mu.Unlock()
					return nil
				}
				// Issue #336: ListIndexes returns ARNs from every region
				// in the account regardless of the SDK client's region.
				// Per-region clients can't tag-fetch foreign ARNs (the
				// API rejects with BadRequestException "expected region
				// X"), and addressBook dedup keys on the outer-loop
				// region — emitting an off-region ARN here would also
				// produce a duplicate ImportedResource when the operator
				// listed the home region. Drop both the tag-fetch and
				// the emission for ARNs whose region != outer-loop.
				if parsed, perr := awsarn.Parse(ix.arn); perr == nil && parsed.Region != "" && parsed.Region != region {
					return nil
				}
				tagsOut, err := client.ListTagsForResource(gctx, &resourceexplorer2.ListTagsForResourceInput{ResourceArn: aws.String(ix.arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: resourceexplorer2_index %s: list tags (region=%s): %v\n", ix.arn, region, err)
					return nil
				}
				tags := tagsOut.Tags
				if tags == nil {
					tags = map[string]string{}
				}
				ix.tags = tags
				mu.Lock()
				ok = append(ok, ix)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool { return ok[i].arn < ok[j].arn })

		for _, ix := range ok {
			if !MatchesAll(ix.tags, args.TagSelectors) {
				continue
			}
			// At most one index per region per account. The ARN's
			// trailing UUID is unstable across recreate cycles and
			// provides no value as a NameHint, so derive a stable
			// region-scoped hint instead.
			name := "index-" + region
			native := map[string]string{
				"arn":    ix.arn,
				"type":   ix.indexType,
				"region": ix.region,
			}
			imps = append(imps, makeImportedResource(
				book,
				re2IndexTFType,
				name,
				ix.arn,
				region,
				args.AccountID,
				native,
				ix.tags,
			))
			args.Emitter.ItemFound(slug, region, re2IndexTFType, ix.arn)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves the Resource Explorer 2 index for the requested
// region. GetIndex takes no arguments — it returns whatever index is
// configured for the SDK client's region. The id parameter is accepted
// for interface conformity and validated as a Resource Explorer 2 ARN
// shape.
func (d *resourceExplorer2IndexDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	if err := validateResourceExplorer2ARN(id); err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.GetIndex(ctx, &resourceexplorer2.GetIndexInput{})
	if err != nil {
		var notFound *re2types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_resourceexplorer2_index %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetIndex: %w", err)
	}
	arn := aws.ToString(out.Arn)
	if arn == "" {
		return imported.ImportedResource{}, fmt.Errorf("aws_resourceexplorer2_index %q: %w", id, ErrNotFound)
	}
	// Stable region-scoped NameHint; see Discover for rationale.
	name := "index-" + region
	native := map[string]string{
		"arn":    arn,
		"type":   string(out.Type),
		"region": region,
	}
	return makeImportedResource(
		addressBook{},
		re2IndexTFType,
		name,
		arn,
		region,
		accountID,
		native,
		nil,
	), nil
}

// re2NameFromArn returns the trailing path segment of a Resource
// Explorer 2 ARN. Resource Explorer index/view ARNs end with a
// UUID-shaped suffix; using the last segment as the NameHint matches
// the convention every other discoverer uses for ARN-only resources
// (no human-readable name field).
func re2NameFromArn(arn string) string {
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i < len(arn)-1 {
		return arn[i+1:]
	}
	return arn
}

// validateResourceExplorer2ARN is a lenient ARN-shape check. The ID
// passed to DiscoverByID is informational — GetIndex doesn't accept an
// ARN parameter — so we just reject obvious garbage.
func validateResourceExplorer2ARN(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("resourceexplorer2_index: empty id: %w", ErrNotSupported)
	}
	if !strings.HasPrefix(id, "arn:") {
		return fmt.Errorf("resourceexplorer2_index: id %q is not an ARN: %w", id, ErrNotSupported)
	}
	if !strings.Contains(id, ":resource-explorer-2:") {
		return fmt.Errorf("resourceexplorer2_index: id %q is not a resource-explorer-2 ARN: %w", id, ErrNotSupported)
	}
	return nil
}
