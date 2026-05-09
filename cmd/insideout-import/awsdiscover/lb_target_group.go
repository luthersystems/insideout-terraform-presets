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
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	lbTargetGroupTFType    = "aws_lb_target_group"
	lbTargetGroupAssetType = "elasticloadbalancing:targetgroup"
	lbTargetGroupSlug      = "lb_target_group"
)

// lbTargetGroupClient is the narrow subset of the ELBv2 SDK the
// target-group discoverer uses. DescribeTargetGroups does not return
// tags inline; per-TG DescribeTags is fanned out under errgroup.
type lbTargetGroupClient interface {
	DescribeTargetGroups(ctx context.Context, in *elasticloadbalancingv2.DescribeTargetGroupsInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupsOutput, error)
	DescribeTags(ctx context.Context, in *elasticloadbalancingv2.DescribeTagsInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error)
}

type lbTargetGroupDiscoverer struct {
	new            func(region string) lbTargetGroupClient
	maxConcurrency int
}

func newLBTargetGroupDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &lbTargetGroupDiscoverer{
		new: func(region string) lbTargetGroupClient {
			return elasticloadbalancingv2.NewFromConfig(cfg, func(o *elasticloadbalancingv2.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *lbTargetGroupDiscoverer) ResourceType() string { return lbTargetGroupTFType }

// Discover paginates DescribeTargetGroups (Marker pagination) and filters
// client-side by target-group name prefix matching args.Project. ELBv2
// does not expose a server-side name-prefix filter, so the prefix check
// happens after each page.
//
// Tags are NOT inline on DescribeTargetGroups responses — for each
// prefix-matching TG we issue DescribeTags(ResourceArns=[arn]) under a
// bounded errgroup. Per-TG DescribeTags failures stay fail-closed (skip
// the row, surface a stderr WARN). Parent-context cancellation IS
// propagated.
//
// Operator-supplied TagSelectors are AND'd via MatchesAll AFTER the tag
// fetch.
//
// Multi-region (#291): outer loop walks args.Regions. ImportID is the
// target-group ARN.
func (d *lbTargetGroupDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var imps []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(lbTargetGroupSlug, region)
		regionCount := 0
		client := d.new(region)

		var matched []elbv2types.TargetGroup
		input := &elasticloadbalancingv2.DescribeTargetGroupsInput{}
		for {
			out, err := client.DescribeTargetGroups(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(lbTargetGroupSlug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("DescribeTargetGroups (region=%s): %w", region, err)
			}
			for i := range out.TargetGroups {
				tg := out.TargetGroups[i]
				if args.Project != "" && !strings.HasPrefix(aws.ToString(tg.TargetGroupName), args.Project) {
					continue
				}
				matched = append(matched, tg)
			}
			if out.NextMarker == nil || *out.NextMarker == "" {
				break
			}
			input.Marker = out.NextMarker
		}

		type entry struct {
			tg   elbv2types.TargetGroup
			tags map[string]string
		}
		var (
			mu sync.Mutex
			ok []entry
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for i := range matched {
			tg := matched[i]
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				arn := aws.ToString(tg.TargetGroupArn)
				tagsOut, err := client.DescribeTags(gctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: []string{arn}})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: lb_target_group %s: describe tags (region=%s): %v\n", aws.ToString(tg.TargetGroupName), region, err)
					return nil
				}
				tags := elbv2TagsFor(arn, tagsOut.TagDescriptions)
				mu.Lock()
				ok = append(ok, entry{tg: tg, tags: tags})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(lbTargetGroupSlug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeTags (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool {
			return aws.ToString(ok[i].tg.TargetGroupName) < aws.ToString(ok[j].tg.TargetGroupName)
		})

		for _, e := range ok {
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			arn := aws.ToString(e.tg.TargetGroupArn)
			name := aws.ToString(e.tg.TargetGroupName)
			native := map[string]string{
				"target_group_arn":  arn,
				"target_group_name": name,
				"vpc_id":            aws.ToString(e.tg.VpcId),
				"protocol":          string(e.tg.Protocol),
				"port":              fmt.Sprintf("%d", aws.ToInt32(e.tg.Port)),
			}
			imps = append(imps, makeImportedResource(
				book,
				lbTargetGroupTFType,
				name,
				arn,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(lbTargetGroupSlug, region, lbTargetGroupTFType, arn)
			regionCount++
		}
		args.Emitter.ServiceFinish(lbTargetGroupSlug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves a target group by ARN
// (arn:aws:elasticloadbalancing:<region>:<account>:targetgroup/<name>/<id>)
// or bare TG name. Issues a single DescribeTargetGroups call.
func (d *lbTargetGroupDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("lb_target_group: empty id: %w", ErrNotSupported)
	}
	client := d.new(region)
	in := &elasticloadbalancingv2.DescribeTargetGroupsInput{}
	switch {
	case awsarn.IsARN(id):
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return imported.ImportedResource{}, fmt.Errorf("lb_target_group: parse arn: %w", err)
		}
		if parsed.Service != "elasticloadbalancing" {
			return imported.ImportedResource{}, fmt.Errorf("lb_target_group: not an elasticloadbalancing arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		if !strings.HasPrefix(parsed.Resource, "targetgroup/") {
			return imported.ImportedResource{}, fmt.Errorf("lb_target_group: arn resource %q is not targetgroup/<name>/<id>: %w", parsed.Resource, ErrNotSupported)
		}
		in.TargetGroupArns = []string{id}
	case strings.ContainsAny(id, " :/"):
		return imported.ImportedResource{}, fmt.Errorf("lb_target_group: unrecognized id %q: %w", id, ErrNotSupported)
	default:
		in.Names = []string{id}
	}
	out, err := client.DescribeTargetGroups(ctx, in)
	if err != nil {
		var notFound *elbv2types.TargetGroupNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_lb_target_group %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeTargetGroups: %w", err)
	}
	if len(out.TargetGroups) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_lb_target_group %q: %w", id, ErrNotFound)
	}
	tg := out.TargetGroups[0]
	arn := aws.ToString(tg.TargetGroupArn)
	name := aws.ToString(tg.TargetGroupName)
	native := map[string]string{
		"target_group_arn":  arn,
		"target_group_name": name,
		"vpc_id":            aws.ToString(tg.VpcId),
		"protocol":          string(tg.Protocol),
		"port":              fmt.Sprintf("%d", aws.ToInt32(tg.Port)),
	}
	return makeImportedResource(
		addressBook{},
		lbTargetGroupTFType,
		name,
		arn,
		region,
		accountID,
		native,
		nil,
	), nil
}
