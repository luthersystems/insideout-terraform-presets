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
	lbListenerTFType    = "aws_lb_listener"
	lbListenerAssetType = "elasticloadbalancing:listener"
	lbListenerSlug      = "lb_listener"
)

// lbListenerClient is the narrow subset of the ELBv2 SDK the listener
// discoverer uses. Listing listeners is a two-step pattern: first
// DescribeLoadBalancers to find prefix-matching LBs, then per-LB
// DescribeListeners (the API requires LoadBalancerArn). Tags are not
// inline on DescribeListeners — per-listener DescribeTags fans out
// under errgroup.
type lbListenerClient interface {
	DescribeLoadBalancers(ctx context.Context, in *elasticloadbalancingv2.DescribeLoadBalancersInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error)
	DescribeListeners(ctx context.Context, in *elasticloadbalancingv2.DescribeListenersInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeListenersOutput, error)
	DescribeTags(ctx context.Context, in *elasticloadbalancingv2.DescribeTagsInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error)
}

type lbListenerDiscoverer struct {
	new            func(region string) lbListenerClient
	maxConcurrency int
}

func newLBListenerDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &lbListenerDiscoverer{
		new: func(region string) lbListenerClient {
			return elasticloadbalancingv2.NewFromConfig(cfg, func(o *elasticloadbalancingv2.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *lbListenerDiscoverer) ResourceType() string { return lbListenerTFType }

// Discover lists listeners for every load balancer whose name passes
// args.Project's prefix filter:
//
//  1. Paginate DescribeLoadBalancers and prefix-filter client-side (LB
//     name).
//  2. For each prefix-matching LB, call DescribeListeners with that
//     LoadBalancerArn (DescribeListeners requires LoadBalancerArn).
//     Listener-listing fans out across LBs under a bounded errgroup.
//  3. Per-listener DescribeTags fan-out (also errgroup-bound) for tag
//     fetch.
//
// Operator-supplied TagSelectors are AND'd via MatchesAll AFTER the tag
// fetch.
//
// Multi-region (#291): outer loop walks args.Regions. ImportID is the
// listener ARN.
func (d *lbListenerDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	var imps []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(lbListenerSlug, region)
		regionCount := 0
		client := d.new(region)

		// Step 1: list LBs and prefix-filter.
		type lbHit struct {
			arn  string
			name string
		}
		var lbs []lbHit
		input := &elasticloadbalancingv2.DescribeLoadBalancersInput{}
		for {
			out, err := client.DescribeLoadBalancers(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(lbListenerSlug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("DescribeLoadBalancers (region=%s): %w", region, err)
			}
			for _, lb := range out.LoadBalancers {
				name := aws.ToString(lb.LoadBalancerName)
				if args.Project != "" && !strings.HasPrefix(name, args.Project) {
					continue
				}
				lbs = append(lbs, lbHit{arn: aws.ToString(lb.LoadBalancerArn), name: name})
			}
			if out.NextMarker == nil || *out.NextMarker == "" {
				break
			}
			input.Marker = out.NextMarker
		}

		// Step 2: per-LB DescribeListeners under errgroup.
		type lnEntry struct {
			ln     elbv2types.Listener
			lbName string
		}
		var (
			mu1      sync.Mutex
			listings []lnEntry
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g1, gctx1 := errgroup.WithContext(ctx)
		g1.SetLimit(limit)
		for _, h := range lbs {
			h := h
			g1.Go(func() error {
				if err := gctx1.Err(); err != nil {
					return err
				}
				lnInput := &elasticloadbalancingv2.DescribeListenersInput{LoadBalancerArn: aws.String(h.arn)}
				for {
					out, err := client.DescribeListeners(gctx1, lnInput)
					if err != nil {
						return fmt.Errorf("DescribeListeners (lb=%s): %w", h.name, err)
					}
					mu1.Lock()
					for _, ln := range out.Listeners {
						listings = append(listings, lnEntry{ln: ln, lbName: h.name})
					}
					mu1.Unlock()
					if out.NextMarker == nil || *out.NextMarker == "" {
						break
					}
					lnInput.Marker = out.NextMarker
				}
				return nil
			})
		}
		if err := g1.Wait(); err != nil {
			args.Emitter.ServiceFinish(lbListenerSlug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeListeners (region=%s): %w", region, err)
		}

		// Step 3: per-listener DescribeTags under errgroup.
		type entry struct {
			ln     elbv2types.Listener
			lbName string
			tags   map[string]string
		}
		var (
			mu2 sync.Mutex
			ok  []entry
		)
		g2, gctx2 := errgroup.WithContext(ctx)
		g2.SetLimit(limit)
		for i := range listings {
			it := listings[i]
			g2.Go(func() error {
				if err := gctx2.Err(); err != nil {
					return err
				}
				arn := aws.ToString(it.ln.ListenerArn)
				tagsOut, err := client.DescribeTags(gctx2, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: []string{arn}})
				if err != nil {
					if cerr := gctx2.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: lb_listener %s (lb=%s): describe tags (region=%s): %v\n", arn, it.lbName, region, err)
					return nil
				}
				tags := elbv2TagsFor(arn, tagsOut.TagDescriptions)
				mu2.Lock()
				ok = append(ok, entry{ln: it.ln, lbName: it.lbName, tags: tags})
				mu2.Unlock()
				return nil
			})
		}
		if err := g2.Wait(); err != nil {
			args.Emitter.ServiceFinish(lbListenerSlug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("DescribeTags (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool {
			return aws.ToString(ok[i].ln.ListenerArn) < aws.ToString(ok[j].ln.ListenerArn)
		})

		for _, e := range ok {
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			arn := aws.ToString(e.ln.ListenerArn)
			port := aws.ToInt32(e.ln.Port)
			name := fmt.Sprintf("%s-%d", e.lbName, port)
			native := map[string]string{
				"listener_arn": arn,
				"lb_arn":       aws.ToString(e.ln.LoadBalancerArn),
				"protocol":     string(e.ln.Protocol),
				"port":         fmt.Sprintf("%d", port),
			}
			imps = append(imps, makeImportedResource(
				book,
				lbListenerTFType,
				name,
				arn,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(lbListenerSlug, region, lbListenerTFType, arn)
			regionCount++
		}
		args.Emitter.ServiceFinish(lbListenerSlug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves a listener by ARN
// (arn:aws:elasticloadbalancing:<region>:<account>:listener/app/<name>/<lb-id>/<listener-id>).
// Bare names are not accepted — listeners have no name field, only ARN
// + integer port. Issues a single DescribeListeners call.
func (d *lbListenerDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("lb_listener: empty id: %w", ErrNotSupported)
	}
	if !awsarn.IsARN(id) {
		return imported.ImportedResource{}, fmt.Errorf("lb_listener: id %q is not an ARN (listeners have no bare-name shape): %w", id, ErrNotSupported)
	}
	parsed, err := awsarn.Parse(id)
	if err != nil {
		return imported.ImportedResource{}, fmt.Errorf("lb_listener: parse arn: %w", err)
	}
	if parsed.Service != "elasticloadbalancing" {
		return imported.ImportedResource{}, fmt.Errorf("lb_listener: not an elasticloadbalancing arn (service=%q): %w", parsed.Service, ErrNotSupported)
	}
	if !strings.HasPrefix(parsed.Resource, "listener/") {
		return imported.ImportedResource{}, fmt.Errorf("lb_listener: arn resource %q is not listener/<lb-spec>/<id>: %w", parsed.Resource, ErrNotSupported)
	}
	client := d.new(region)
	out, err := client.DescribeListeners(ctx, &elasticloadbalancingv2.DescribeListenersInput{ListenerArns: []string{id}})
	if err != nil {
		var notFound *elbv2types.ListenerNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_lb_listener %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeListeners: %w", err)
	}
	if len(out.Listeners) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_lb_listener %q: %w", id, ErrNotFound)
	}
	ln := out.Listeners[0]
	arn := aws.ToString(ln.ListenerArn)
	port := aws.ToInt32(ln.Port)
	// Best-effort name: ARN + port. The discovery loop derives a richer
	// name (lb-name + port) but DiscoverByID does not look up the parent
	// LB — dep-chase only needs the address resolution.
	lbArn := aws.ToString(ln.LoadBalancerArn)
	name := fmt.Sprintf("%s-%d", lbListenerNameFromLBArn(lbArn), port)
	native := map[string]string{
		"listener_arn": arn,
		"lb_arn":       lbArn,
		"protocol":     string(ln.Protocol),
		"port":         fmt.Sprintf("%d", port),
	}
	return makeImportedResource(
		addressBook{},
		lbListenerTFType,
		name,
		arn,
		region,
		accountID,
		native,
		nil,
	), nil
}

// lbListenerNameFromLBArn extracts the LB name segment from a load
// balancer ARN of the shape
// arn:aws:elasticloadbalancing:<region>:<account>:loadbalancer/<scheme>/<name>/<id>
// for use in synthesizing a NameHint. Returns the bare ARN string if
// the shape doesn't parse — DiscoverByID still emits a usable resource.
func lbListenerNameFromLBArn(lbArn string) string {
	if !awsarn.IsARN(lbArn) {
		return lbArn
	}
	parsed, err := awsarn.Parse(lbArn)
	if err != nil {
		return lbArn
	}
	// loadbalancer/{app,net,gwy}/<name>/<id>
	parts := strings.SplitN(parsed.Resource, "/", 4)
	if len(parts) < 3 || parts[0] != "loadbalancer" {
		return lbArn
	}
	return parts[2]
}
