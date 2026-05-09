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
	lbTFType    = "aws_lb"
	lbAssetType = "elasticloadbalancing:loadbalancer"
	lbSlug      = "lb"
)

// lbClient is the narrow subset of the ELBv2 SDK the load-balancer
// discoverer uses. Mirrors the per-service interface convention in this
// package so unit tests can mock the SDK boundary without depending on
// real AWS credentials. DescribeLoadBalancers does NOT return tags
// inline (unlike EC2 Describe* calls), so each LB requires a separate
// DescribeTags round-trip — fanned out under a bounded errgroup.
type lbClient interface {
	DescribeLoadBalancers(ctx context.Context, in *elasticloadbalancingv2.DescribeLoadBalancersInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error)
	DescribeTags(ctx context.Context, in *elasticloadbalancingv2.DescribeTagsInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error)
}

type lbDiscoverer struct {
	new            func(region string) lbClient
	maxConcurrency int
}

func newLBDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &lbDiscoverer{
		new: func(region string) lbClient {
			return elasticloadbalancingv2.NewFromConfig(cfg, func(o *elasticloadbalancingv2.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *lbDiscoverer) ResourceType() string { return lbTFType }

// Discover paginates DescribeLoadBalancers (Marker pagination) and
// filters client-side by load-balancer name prefix matching args.Project.
// ELBv2 does not expose a server-side name-prefix filter on
// DescribeLoadBalancers, so the prefix check happens after each page.
//
// Tags are NOT inline on DescribeLoadBalancers responses — for each
// prefix-matching LB we issue a separate DescribeTags(ResourceArns=[arn])
// call under a bounded errgroup so a few-hundred-LB account does not
// serialize the per-LB tag fetches. Per-LB DescribeTags failures stay
// fail-closed (skip the row, surface a stderr WARN) since the SDK
// retryer has already exhausted its budget. Parent-context cancellation
// IS propagated: gctx unblocks any in-flight goroutines.
//
// Operator-supplied TagSelectors are AND'd via MatchesAll AFTER the tag
// fetch so the same code path covers both server-side filtering (which
// ELBv2 does not provide) and client-side AND-of-equality semantics.
//
// Multi-region (#291): outer loop walks args.Regions, building a
// per-region SDK client. ImportID is the LB ARN.
func (d *lbDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var imps []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(lbSlug, region)
		regionCount := 0
		client := d.new(region)

		// Step 1: paginate DescribeLoadBalancers and prefix-filter client-side.
		var matched []elbv2types.LoadBalancer
		input := &elasticloadbalancingv2.DescribeLoadBalancersInput{}
		for {
			out, err := client.DescribeLoadBalancers(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(lbSlug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("DescribeLoadBalancers (region=%s): %w", region, err)
			}
			for i := range out.LoadBalancers {
				lb := out.LoadBalancers[i]
				if args.Project != "" && !strings.HasPrefix(aws.ToString(lb.LoadBalancerName), args.Project) {
					continue
				}
				matched = append(matched, lb)
			}
			if out.NextMarker == nil || *out.NextMarker == "" {
				break
			}
			input.Marker = out.NextMarker
		}

		// Step 2: fan out per-LB DescribeTags under a bounded errgroup.
		type entry struct {
			lb   elbv2types.LoadBalancer
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
			lb := matched[i]
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				arn := aws.ToString(lb.LoadBalancerArn)
				tagsOut, err := client.DescribeTags(gctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: []string{arn}})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: lb %s: describe tags (region=%s): %v\n", aws.ToString(lb.LoadBalancerName), region, err)
					return nil
				}
				tags := elbv2TagsFor(arn, tagsOut.TagDescriptions)
				mu.Lock()
				ok = append(ok, entry{lb: lb, tags: tags})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(lbSlug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeTags (region=%s): %w", region, err)
		}

		// Sort deterministically by LB name so the emitted manifest is
		// stable across runs.
		sort.Slice(ok, func(i, j int) bool {
			return aws.ToString(ok[i].lb.LoadBalancerName) < aws.ToString(ok[j].lb.LoadBalancerName)
		})

		for _, e := range ok {
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			arn := aws.ToString(e.lb.LoadBalancerArn)
			name := aws.ToString(e.lb.LoadBalancerName)
			native := map[string]string{
				"lb_arn":   arn,
				"lb_name":  name,
				"dns_name": aws.ToString(e.lb.DNSName),
				"vpc_id":   aws.ToString(e.lb.VpcId),
			}
			if t := string(e.lb.Type); t != "" {
				native["type"] = t
			}
			imps = append(imps, makeImportedResource(
				book,
				lbTFType,
				name,
				arn,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(lbSlug, region, lbTFType, arn)
			regionCount++
		}
		args.Emitter.ServiceFinish(lbSlug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// elbv2TagsFor walks a DescribeTags response's TagDescriptions slice and
// returns the tag map for the requested ARN. ELBv2 DescribeTags echoes
// back ResourceArn alongside Tags; we filter on it instead of trusting
// position. Returns a non-nil empty map for "fetched but no tags" so the
// nil-vs-empty distinction stays load-bearing for selector matching.
func elbv2TagsFor(arn string, descs []elbv2types.TagDescription) map[string]string {
	tags := map[string]string{}
	for _, td := range descs {
		if aws.ToString(td.ResourceArn) != arn {
			continue
		}
		for _, t := range td.Tags {
			tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
	}
	return tags
}

// DiscoverByID resolves a load balancer by ARN
// (arn:aws:elasticloadbalancing:<region>:<account>:loadbalancer/app/<name>/<id>)
// or bare LB name. Issues a single DescribeLoadBalancers call to verify
// existence; pass nil tags (DiscoverByID does not fetch tags).
func (d *lbDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("lb: empty id: %w", ErrNotSupported)
	}
	client := d.new(region)
	in := &elasticloadbalancingv2.DescribeLoadBalancersInput{}
	switch {
	case awsarn.IsARN(id):
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return imported.ImportedResource{}, fmt.Errorf("lb: parse arn: %w", err)
		}
		if parsed.Service != "elasticloadbalancing" {
			return imported.ImportedResource{}, fmt.Errorf("lb: not an elasticloadbalancing arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// Resource shape: loadbalancer/app/<name>/<id> or loadbalancer/net/<name>/<id>
		// or loadbalancer/<name> (classic-ELB shape — not aws_lb).
		if !strings.HasPrefix(parsed.Resource, "loadbalancer/app/") &&
			!strings.HasPrefix(parsed.Resource, "loadbalancer/net/") &&
			!strings.HasPrefix(parsed.Resource, "loadbalancer/gwy/") {
			return imported.ImportedResource{}, fmt.Errorf("lb: arn resource %q is not loadbalancer/{app,net,gwy}/<name>/<id>: %w", parsed.Resource, ErrNotSupported)
		}
		in.LoadBalancerArns = []string{id}
	case strings.ContainsAny(id, " :/"):
		return imported.ImportedResource{}, fmt.Errorf("lb: unrecognized id %q: %w", id, ErrNotSupported)
	default:
		in.Names = []string{id}
	}
	out, err := client.DescribeLoadBalancers(ctx, in)
	if err != nil {
		var notFound *elbv2types.LoadBalancerNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_lb %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeLoadBalancers: %w", err)
	}
	if len(out.LoadBalancers) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_lb %q: %w", id, ErrNotFound)
	}
	lb := out.LoadBalancers[0]
	arn := aws.ToString(lb.LoadBalancerArn)
	name := aws.ToString(lb.LoadBalancerName)
	native := map[string]string{
		"lb_arn":   arn,
		"lb_name":  name,
		"dns_name": aws.ToString(lb.DNSName),
		"vpc_id":   aws.ToString(lb.VpcId),
	}
	if t := string(lb.Type); t != "" {
		native["type"] = t
	}
	return makeImportedResource(
		addressBook{},
		lbTFType,
		name,
		arn,
		region,
		accountID,
		native,
		nil,
	), nil
}
