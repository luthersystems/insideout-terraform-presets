package awsdiscover

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// sdkOnlySubresourceConfig is the per-Terraform-type contract for routing
// a resource type through the SDK-only sub-resource discoverer (Bundle
// 14k1, issue #452). It is the sibling of cloudControlConfig for
// Terraform-only synthetic resources whose Cloud Control type either
// doesn't exist (e.g. AWS::S3::Bucket models versioning / lifecycle /
// ownership / publicAccessBlock / encryption as inline bucket properties
// rather than standalone resource types) or whose CC API returns
// TypeNotFoundException on describe-type probes (verified live for the 5
// S3 sub-resources covered by this bundle).
//
// Both enumeration and per-item read happen via service-native AWS SDK
// calls. The existing cloudControlConfig.SDKLister field (Bundle 14b,
// PR #418) only seeded the CC GetResource fan-out — that's not enough
// here because there's also no CC GetResource for these types.
//
// Field semantics:
//
//   - TFType: Terraform-side resource type identifier (e.g.
//     "aws_s3_bucket_versioning"). Required.
//   - Slug: progress-event service slug. Used for
//     ServiceStart / ServiceFinish / ItemFound / ServiceWarn emits.
//     Must match the per-type entry in serviceSlugCombined.
//   - ParentCFNType: the CloudFormation type of the parent resource
//     (e.g. "AWS::S3::Bucket"). Consulted via args.RGTCacheForCFN to
//     skip parent enumeration when the prefetch cache is warm.
//   - IsGlobal: when true, the discoverer issues one pass (region="")
//     instead of looping args.Regions. None of the 14k1 S3 sub-resources
//     set this; reserved for future global parent types.
//   - SkipProjectTagFilter: bypasses the legacy Project tag filter for
//     genuinely-untaggable types. All 5 S3 sub-resources are untaggable,
//     so all set this to true. The discoverer also uses this flag to
//     decide whether to consult the RGT-cache for parents (taggable
//     parents come from the cache; untaggable types still need parents
//     so we always fall back to ListParents even when the parent itself
//     is taggable — for S3 buckets the RGT cache surfaces them when
//     --project is supplied, but the cache-miss path runs ListParents).
//   - ListParents: SDK-driven parent enumeration. Called when the
//     RGT-cache for ParentCFNType is empty / absent for the current
//     region. Returns the parent identifiers (e.g. bucket names) used
//     to drive the per-item FetchItem fan-out. Required.
//   - FetchItem: per-parent SDK call that reads the sub-resource state.
//     Returns (exists, props, nativeIDs, err). When the sub-resource is
//     unset for this parent — typically signaled by a service-native
//     NotFound error code (NoSuchVersioningConfiguration,
//     NoSuchLifecycleConfiguration, OwnershipControlsNotFoundError,
//     NoSuchPublicAccessBlockConfiguration,
//     ServerSideEncryptionConfigurationNotFoundError) — FetchItem must
//     return (false, nil, nil, nil) rather than an error so the parent
//     is silently skipped. Any other error is propagated through a
//     ServiceWarn and skips that parent (soft-fail symmetric with
//     cloudControlDiscoverer's per-item GetResource posture). Required.
//   - ImportIDFromParent: converts a parent identifier (and the
//     FetchItem-returned properties) into the Terraform import ID format.
//     For all 5 S3 sub-resources this is the bare bucket name. Required.
//   - NameHintFromParent: produces a human-readable name hint suitable
//     for Identity.NameHint and the address generator's hint precedence.
//     Convention for sub-resources: "<parent-id>-<sub-resource-slug>"
//     so downstream summaries distinguish a bucket's versioning from
//     its lifecycle configuration. Required.
type sdkOnlySubresourceConfig struct {
	TFType               string
	Slug                 string
	ParentCFNType        string
	IsGlobal             bool
	SkipProjectTagFilter bool
	ListParents          func(ctx context.Context, awsCfg aws.Config, region string, args DiscoverArgs) ([]string, error)
	FetchItem            func(ctx context.Context, awsCfg aws.Config, region, parentID string) (exists bool, props map[string]any, nativeIDs map[string]string, err error)
	ImportIDFromParent   func(parentID string, props map[string]any) string
	NameHintFromParent   func(parentID string, props map[string]any) string
}

// sdkOnlySubresourceDiscoverer is the generic per-type Discoverer that
// routes a Terraform sub-resource type through service-native SDK calls.
// One instance is constructed per registered TFType; the per-type
// behavior lives entirely in cfg (see sdkOnlySubresourceConfig).
//
// The structure mirrors cloudControlDiscoverer so the two pipelines emit
// symmetric observability (ServiceStart/Finish/Warn/ItemFound) and the
// same ImportedResource shape via makeImportedResource. Per-item
// FetchItem failures soft-fail through ServiceWarn so a single throttled
// or transiently-erroring parent does not abort the whole region's
// scope — matches the cloudControlDiscoverer per-item GetResource
// posture documented in cloudcontrol_discoverer.go:185-187.
type sdkOnlySubresourceDiscoverer struct {
	cfg            sdkOnlySubresourceConfig
	awsCfg         aws.Config
	maxConcurrency int
}

func newSDKOnlySubresourceDiscoverer(cfg sdkOnlySubresourceConfig, awsCfg aws.Config, maxConcurrency int) *sdkOnlySubresourceDiscoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &sdkOnlySubresourceDiscoverer{
		cfg:            cfg,
		awsCfg:         awsCfg,
		maxConcurrency: maxConcurrency,
	}
}

// ResourceType returns the Terraform type this discoverer covers.
func (d *sdkOnlySubresourceDiscoverer) ResourceType() string { return d.cfg.TFType }

// Discover enumerates parents (via RGT cache hit or SDK fallback) and
// fans out per-parent FetchItem calls under a bounded errgroup. Each
// FetchItem that returns exists=true produces one ImportedResource.
// FetchItem errors emit a ServiceWarn and skip that parent. ListParents
// errors abort the region.
//
// Tag filtering: the 5 S3 sub-resources are all untaggable, so
// SkipProjectTagFilter=true bypasses both the RGT short-circuit (RGT
// only sees taggable ARNs) and the post-fetch Project filter. The
// args.TagSelectors filter still applies and (because the tag bag is
// always empty for these types) any non-empty selector list silently
// yields zero items for that type — matching the cloudControlDiscoverer
// posture for untaggable types (see cloudcontrol_discoverer.go:417-426).
func (d *sdkOnlySubresourceDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var out []imported.ImportedResource

	regions := args.Regions
	if d.cfg.IsGlobal {
		regions = []string{""}
	}

	for _, region := range regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(d.cfg.Slug, region)
		regionCount := 0

		parents, err := d.enumerateParents(ctx, region, args)
		if err != nil {
			args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("%s parent enumeration (region=%s): %w", d.cfg.Slug, region, err)
		}
		if len(parents) == 0 {
			args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
			continue
		}

		// Per-parent FetchItem fan-out under bounded errgroup. The
		// emit slice is captured under mu and re-sorted by parentID
		// after Wait so emit order is deterministic regardless of
		// goroutine scheduling. Matches the cloudControlDiscoverer
		// sort-by-identifier convention at cloudcontrol_discoverer.go:408.
		type fetched struct {
			parentID  string
			props     map[string]any
			nativeIDs map[string]string
		}
		var (
			mu   sync.Mutex
			done []fetched
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, parentID := range parents {
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				exists, props, native, ferr := d.cfg.FetchItem(gctx, d.awsCfg, region, parentID)
				if ferr != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					args.Emitter.ServiceWarn(d.cfg.Slug, region,
						fmt.Sprintf("FetchItem %s parent=%q: %v", d.cfg.TFType, parentID, ferr))
					return nil
				}
				if !exists {
					return nil
				}
				mu.Lock()
				done = append(done, fetched{parentID: parentID, props: props, nativeIDs: native})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("%s FetchItem (region=%s): %w", d.cfg.Slug, region, err)
		}

		sort.Slice(done, func(i, j int) bool { return done[i].parentID < done[j].parentID })

		// args.TagSelectors evaluation is hoisted out of the per-item
		// loop because the tag bag is invariant across parents (all
		// sub-resources route through the framework with an empty
		// tag map — they are untaggable). MatchesAll with a non-empty
		// selector list silently drops every item; equivalent to
		// cloudControlDiscoverer.go:427-429 done once per region.
		if !MatchesAll(map[string]string{}, args.TagSelectors) {
			args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
			continue
		}
		for _, f := range done {
			importID := f.parentID
			if d.cfg.ImportIDFromParent != nil {
				importID = d.cfg.ImportIDFromParent(f.parentID, f.props)
			}
			name := f.parentID
			if d.cfg.NameHintFromParent != nil {
				name = d.cfg.NameHintFromParent(f.parentID, f.props)
			}
			out = append(out, makeImportedResource(
				book,
				d.cfg.TFType,
				name,
				importID,
				region,
				args.AccountID,
				f.nativeIDs,
				map[string]string{}, // untaggable: non-nil empty map per #289 gap-#6 nil-vs-empty contract
			))
			args.Emitter.ItemFound(d.cfg.Slug, region, d.cfg.TFType, importID)
			regionCount++
		}
		args.Emitter.ServiceFinish(d.cfg.Slug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
}

// enumerateParents returns the parent identifier set for one region.
// Strategy: when SkipProjectTagFilter is unset and an RGT cache exists
// for ParentCFNType in this region, use the cached identifiers (RGT
// already filtered server-side by args.Project / args.TagSelectors).
// Otherwise call ListParents.
//
// For all 5 14k1 S3 sub-resources SkipProjectTagFilter=true, so the
// RGT-cache short-circuit is skipped and ListParents always runs.
// This matches the cloudControlDiscoverer untaggable-type posture
// documented in cloudcontrol_discoverer.go:233-238 — RGT only sees
// tagged ARNs, but here the sub-resource ITSELF is untaggable while
// the parent (S3 bucket) IS taggable. We could optimize by consulting
// the parent's RGT cache, but the sub-resource discoverer is
// intentionally agnostic to parent taggability so the same code path
// handles future families whose parents are also untaggable.
func (d *sdkOnlySubresourceDiscoverer) enumerateParents(ctx context.Context, region string, args DiscoverArgs) ([]string, error) {
	if !d.cfg.SkipProjectTagFilter && d.cfg.ParentCFNType != "" {
		var (
			cached []arnInfo
			ok     bool
		)
		if d.cfg.IsGlobal {
			cached, ok = args.RGTCacheForGlobalCFN(d.cfg.ParentCFNType)
		} else {
			cached, ok = args.RGTCacheForCFN(region, d.cfg.ParentCFNType)
		}
		if ok && len(cached) > 0 {
			ids := make([]string, 0, len(cached))
			for _, info := range cached {
				ids = append(ids, info.Identifier)
			}
			return ids, nil
		}
		// Fall through to ListParents on cache miss or empty bucket.
		// (An empty taggable bucket might mean "no parents matched
		// the project tag" — but for the untaggable sub-resources we
		// can't observe that distinction, so we err on the side of
		// running ListParents and letting the operator's
		// --tag-selector filter ride through args.TagSelectors.)
	}
	if d.cfg.ListParents == nil {
		return nil, fmt.Errorf("%s: ListParents must be set on sdkOnlySubresourceConfig", d.cfg.TFType)
	}
	return d.cfg.ListParents(ctx, d.awsCfg, region, args)
}

// DiscoverByID resolves a single sub-resource by its parent identifier
// (which equals its import ID for all 5 14k1 S3 sub-resources — the
// bucket name is the only addressing dimension). Used by Stage 2c3's
// dep-chase loop.
//
// FetchItem-returned exists=false maps to ErrNotFound. An empty id maps
// to ErrNotSupported so dep-chase can route to a sibling discoverer.
// Any other FetchItem error is propagated unwrapped — DiscoverByID
// does not soft-fail (unlike the bulk Discover path) because the
// caller asked for one specific resource and a transient error
// shouldn't masquerade as "resource doesn't exist."
func (d *sdkOnlySubresourceDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("%s: empty id: %w", d.cfg.TFType, ErrNotSupported)
	}
	exists, props, native, err := d.cfg.FetchItem(ctx, d.awsCfg, region, id)
	if err != nil {
		return imported.ImportedResource{}, fmt.Errorf("FetchItem %s parent=%q: %w", d.cfg.TFType, id, err)
	}
	if !exists {
		return imported.ImportedResource{}, fmt.Errorf("%s parent=%q: %w", d.cfg.TFType, id, ErrNotFound)
	}
	importID := id
	if d.cfg.ImportIDFromParent != nil {
		importID = d.cfg.ImportIDFromParent(id, props)
	}
	name := id
	if d.cfg.NameHintFromParent != nil {
		name = d.cfg.NameHintFromParent(id, props)
	}
	return makeImportedResource(
		addressBook{},
		d.cfg.TFType,
		name,
		importID,
		region,
		accountID,
		native,
		map[string]string{},
	), nil
}
