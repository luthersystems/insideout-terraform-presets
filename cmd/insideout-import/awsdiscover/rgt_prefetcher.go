package awsdiscover

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgttypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// rgtClient is the narrow subset of AWS Resource Groups Tagging API that
// the prefetcher uses. Fakes injected by unit tests satisfy this same
// interface; production code constructs a real *resourcegroupstaggingapi.Client.
type rgtClient interface {
	GetResources(ctx context.Context,
		in *resourcegroupstaggingapi.GetResourcesInput,
		opts ...func(*resourcegroupstaggingapi.Options),
	) (*resourcegroupstaggingapi.GetResourcesOutput, error)
}

// arnInfo is the prefetcher's per-ARN datum. ARN is the raw RGT-returned
// resource ARN; Identifier is the pre-computed Cloud Control primary
// identifier produced by the matched arnRule (so downstream
// cloudControlDiscoverer doesn't re-parse the ARN); Tags is the
// authoritative tag map RGT returned (no need to re-extract from the
// CloudFormation properties payload).
type arnInfo struct {
	ARN        string
	Identifier string
	Tags       map[string]string
}

// rgtCache is the per-call ARN index built by RGTPrefetcher.Prefetch.
// Keys: cache[region][cfnType] -> sorted slice of arnInfo. Callers ask
// "do you have ARNs for (region, cfnType)?" via ForCFN; global-service
// types ask via ForGlobalCFN which de-dupes across the per-region
// buckets (IAM/CloudFront/Route53 ARNs surface in every region's RGT
// response so the un-deduped union would emit the same resource N
// times).
//
// A nil rgtCache signals "no prefetch ran" — see Prefetch's contract.
// All accessor methods handle nil receivers gracefully so callers can
// avoid nil-checking in the common path.
type rgtCache struct {
	byRegionAndType map[string]map[string][]arnInfo
}

// ForCFN returns the ARNs prefetched for (region, cfnType), and ok=true
// if the prefetcher saw at least one ARN for that bucket. A cache miss
// (ok=false) signals the caller to fall back to its own per-type
// ListResources. nil cache always returns ok=false.
func (c *rgtCache) ForCFN(region, cfnType string) ([]arnInfo, bool) {
	if c == nil || c.byRegionAndType == nil {
		return nil, false
	}
	byType, ok := c.byRegionAndType[region]
	if !ok {
		return nil, false
	}
	infos, ok := byType[cfnType]
	if !ok {
		return nil, false
	}
	return infos, true
}

// ForGlobalCFN returns the de-duplicated union of ARNs across every
// region bucket. Used by the discoverer for global CloudFormation types
// (e.g. AWS::IAM::Role, AWS::CloudFront::Distribution) whose ARNs RGT
// returns in every region's response. ok=true when at least one ARN
// was found.
func (c *rgtCache) ForGlobalCFN(cfnType string) ([]arnInfo, bool) {
	if c == nil || c.byRegionAndType == nil {
		return nil, false
	}
	seen := map[string]struct{}{}
	var out []arnInfo
	regions := make([]string, 0, len(c.byRegionAndType))
	for r := range c.byRegionAndType {
		regions = append(regions, r)
	}
	sort.Strings(regions)
	for _, r := range regions {
		for _, info := range c.byRegionAndType[r][cfnType] {
			if _, dup := seen[info.ARN]; dup {
				continue
			}
			seen[info.ARN] = struct{}{}
			out = append(out, info)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// rgtPrefetcher is implemented by the production realRGTPrefetcher and
// by test fakes. Prefetch issues one GetResources call (paginated) per
// region in args.Regions, server-side-filtered by args.Project and
// args.TagSelectors. The returned rgtCache is consulted by
// cloudControlDiscoverer.Discover before its own per-type ListResources
// fan-out.
//
// Contract for no-op cases:
//   - If both args.Project and args.TagSelectors are empty, Prefetch
//     returns (nil, nil) — RGT with no TagFilters returns the entire
//     account, which is wasteful when the caller will already iterate
//     per-type. Callers treat nil cache as "skip; fall back to
//     per-type ListResources."
//   - Per-region API failure does NOT fail the whole Prefetch.
//     Instead, the prefetcher emits args.Emitter.ServiceWarn(slug="rgt", region, err)
//     and omits that region from the cache. The cloudControlDiscoverer's
//     ForCFN miss path falls back to ListResources for that region.
type rgtPrefetcher interface {
	Prefetch(ctx context.Context, regions []string, args DiscoverArgs) (*rgtCache, error)
}

// realRGTPrefetcher is the production implementation. Per-region RGT
// clients are constructed on demand via the new closure (mirrors the
// cloudControlDiscoverer pattern at cloudcontrol_discoverer.go:104-119).
type realRGTPrefetcher struct {
	new func(region string) rgtClient
}

func newRealRGTPrefetcher(awsCfg aws.Config) *realRGTPrefetcher {
	return &realRGTPrefetcher{
		new: func(region string) rgtClient {
			return resourcegroupstaggingapi.NewFromConfig(awsCfg, func(o *resourcegroupstaggingapi.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
	}
}

// Prefetch implements rgtPrefetcher.
func (p *realRGTPrefetcher) Prefetch(ctx context.Context, regions []string, args DiscoverArgs) (*rgtCache, error) {
	filters := buildTagFilters(args)
	if len(filters) == 0 {
		// No-op: caller falls back to per-type ListResources.
		return nil, nil
	}

	emitter := emitterOrNop(args.Emitter)

	type regionResult struct {
		region string
		byType map[string][]arnInfo
		// unmapped is the set of (service, resourceType) pairs that
		// had no arnRule match — surfaced once per pair across all
		// regions so a fleet of un-mapped resources doesn't drown
		// the warn stream.
		unmapped map[string]struct{}
	}
	results := make([]regionResult, 0, len(regions))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, region := range regions {
		wg.Add(1)
		go func(region string) {
			defer wg.Done()
			byType, unmapped, err := p.fetchRegion(ctx, region, filters)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				emitter.ServiceWarn("rgt", region, fmt.Sprintf("rgt prefetch %q: %v", region, err))
				return
			}
			results = append(results, regionResult{region: region, byType: byType, unmapped: unmapped})
		}(region)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Surface unmapped (service, resourceType) pairs once globally so the
	// caller can extend the rules table. Aggregate across regions before
	// emitting so the same unmapped service in 4 regions warns once.
	unmappedGlobal := map[string]struct{}{}
	for _, r := range results {
		for k := range r.unmapped {
			unmappedGlobal[k] = struct{}{}
		}
	}
	if len(unmappedGlobal) > 0 {
		keys := make([]string, 0, len(unmappedGlobal))
		for k := range unmappedGlobal {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			emitter.ServiceWarn("rgt", "", fmt.Sprintf("rgt: no arnRule for %s (resource skipped; extend cmd/insideout-import/awsdiscover/arn_rules.go)", k))
		}
	}

	cache := &rgtCache{byRegionAndType: make(map[string]map[string][]arnInfo, len(results))}
	for _, r := range results {
		if len(r.byType) == 0 {
			continue
		}
		// Sort each cfnType bucket by ARN for deterministic downstream
		// emit order (the cloudControlDiscoverer downstream sorts by
		// identifier — sorting by ARN here aligns the orderings since
		// the ARN is the source-of-truth identity).
		for _, infos := range r.byType {
			sort.SliceStable(infos, func(i, j int) bool { return infos[i].ARN < infos[j].ARN })
		}
		cache.byRegionAndType[r.region] = r.byType
	}
	return cache, nil
}

// fetchRegion paginates one RGT GetResources call for a single region.
// Returns the per-cfnType ARN map and the (service/resourceType) set of
// unmapped ARNs encountered. Errors from GetResources propagate to the
// caller, which decides whether to warn-and-omit (per-region) or
// hard-fail (whole prefetch).
func (p *realRGTPrefetcher) fetchRegion(ctx context.Context, region string, filters []rgttypes.TagFilter) (map[string][]arnInfo, map[string]struct{}, error) {
	client := p.new(region)
	byType := map[string][]arnInfo{}
	unmapped := map[string]struct{}{}
	var pageToken *string
	// rgtPaginationMaxPages caps the per-region pagination loop. RGT's
	// default page size is 100 resources, so 100 pages covers up to
	// 10,000 tagged resources per region — well above any realistic
	// per-project footprint. Hitting the cap signals either an RGT
	// server bug (replayed page tokens) or a misconfigured customer
	// account; the prefetcher logs the cap and returns the partial
	// result so the per-type ListResources fallback can complete the
	// scan.
	const rgtPaginationMaxPages = 100
	for page := 0; page < rgtPaginationMaxPages; page++ {
		out, err := client.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
			TagFilters:      filters,
			PaginationToken: pageToken,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, m := range out.ResourceTagMappingList {
			arnStr := aws.ToString(m.ResourceARN)
			parsed, perr := parseARN(arnStr)
			if perr != nil {
				continue
			}
			rule, ok := lookupRule(parsed)
			if !ok {
				unmapped[parsed.service+"/"+parsed.resourceType] = struct{}{}
				continue
			}
			tags := make(map[string]string, len(m.Tags))
			for _, t := range m.Tags {
				tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
			}
			byType[rule.cfnType] = append(byType[rule.cfnType], arnInfo{
				ARN:        arnStr,
				Identifier: rule.identifierFn(parsed),
				Tags:       tags,
			})
		}
		if aws.ToString(out.PaginationToken) == "" {
			return byType, unmapped, nil
		}
		pageToken = out.PaginationToken
	}
	return byType, unmapped, fmt.Errorf("rgt pagination exceeded %d pages in region %q (partial result returned)", rgtPaginationMaxPages, region)
}

// buildTagFilters maps DiscoverArgs (Project + TagSelectors) onto the
// RGT TagFilter input shape. Returns an empty slice when both inputs
// are empty — the caller treats that as "skip prefetch, RGT without
// filters returns the entire account."
func buildTagFilters(args DiscoverArgs) []rgttypes.TagFilter {
	var filters []rgttypes.TagFilter
	if args.Project != "" {
		filters = append(filters, rgttypes.TagFilter{
			Key:    aws.String("Project"),
			Values: []string{args.Project},
		})
	}
	for _, s := range args.TagSelectors {
		filters = append(filters, rgttypes.TagFilter{
			Key:    aws.String(s.Key),
			Values: []string{s.Value},
		})
	}
	return filters
}

// noopRGTPrefetcher returns (nil, nil) unconditionally. Installed when
// AWS config fails to construct a real RGT client — discover continues
// via the per-type ListResources fallback path.
type noopRGTPrefetcher struct{}

func (noopRGTPrefetcher) Prefetch(_ context.Context, _ []string, _ DiscoverArgs) (*rgtCache, error) {
	return nil, nil
}
