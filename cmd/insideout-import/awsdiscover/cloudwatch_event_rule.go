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
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	cloudwatchEventRuleTFType    = "aws_cloudwatch_event_rule"
	cloudwatchEventRuleAssetType = "events:rule"

	// defaultEventBusName is the EventBridge default bus name. ListRules
	// without an EventBusName operates on this bus; the per-row
	// EventBusName field is populated to "default" in that case so the
	// NativeIDs map is unambiguous.
	defaultEventBusName = "default"
)

// cloudwatchEventRuleClient is the narrow subset of the EventBridge SDK
// the rule discoverer uses. Mirrors the per-service interface pattern
// used everywhere else in this package so tests can mock the SDK
// boundary without depending on real AWS credentials.
type cloudwatchEventRuleClient interface {
	ListRules(ctx context.Context, in *eventbridge.ListRulesInput, opts ...func(*eventbridge.Options)) (*eventbridge.ListRulesOutput, error)
	DescribeRule(ctx context.Context, in *eventbridge.DescribeRuleInput, opts ...func(*eventbridge.Options)) (*eventbridge.DescribeRuleOutput, error)
	ListTagsForResource(ctx context.Context, in *eventbridge.ListTagsForResourceInput, opts ...func(*eventbridge.Options)) (*eventbridge.ListTagsForResourceOutput, error)
}

type cloudwatchEventRuleDiscoverer struct {
	new            func(region string) cloudwatchEventRuleClient
	maxConcurrency int
}

func newCloudWatchEventRuleDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &cloudwatchEventRuleDiscoverer{
		new: func(region string) cloudwatchEventRuleClient {
			return eventbridge.NewFromConfig(cfg, func(o *eventbridge.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *cloudwatchEventRuleDiscoverer) ResourceType() string { return cloudwatchEventRuleTFType }

// Discover paginates ListRules per region (default event bus only —
// custom buses would require a separate ListEventBuses sweep, which is
// out of scope here) and filters by rule-name prefix matching
// args.Project. AWS-managed rules whose name does not match the
// project prefix (e.g. AutoScalingManagedRule) are intentionally
// dropped — they belong to the AWS service that created them, not the
// InsideOut stack.
//
// Per-rule ListTagsForResource fetches the rule's tag map under a
// bounded errgroup. Per-item failures are fail-closed (transient
// errors skip the rule with a stderr WARN); ListRules errors abort
// the region. Parent-context cancellation is propagated via gctx.
//
// Import ID for aws_cloudwatch_event_rule on the default bus is the
// bare rule name. Custom event buses use "<bus>/<name>" but this
// discoverer scans only the default bus today.
func (d *cloudwatchEventRuleDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	const slug = "cloudwatch_event_rule"
	book := addressBook{}
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		type rule struct {
			name string
			arn  string
			bus  string
			tags map[string]string
		}
		var allRules []rule

		input := &eventbridge.ListRulesInput{}
		for {
			out, err := client.ListRules(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListRules (region=%s): %w", region, err)
			}
			for _, r := range out.Rules {
				name := aws.ToString(r.Name)
				if args.Project != "" && !strings.HasPrefix(name, args.Project) {
					continue
				}
				bus := aws.ToString(r.EventBusName)
				if bus == "" {
					bus = defaultEventBusName
				}
				allRules = append(allRules, rule{
					name: name,
					arn:  aws.ToString(r.Arn),
					bus:  bus,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		// Per-rule tag fetch under bounded errgroup.
		var (
			mu sync.Mutex
			ok []rule
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, r := range allRules {
			r := r
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				if r.arn == "" {
					mu.Lock()
					ok = append(ok, r)
					mu.Unlock()
					return nil
				}
				tagsOut, err := client.ListTagsForResource(gctx, &eventbridge.ListTagsForResourceInput{ResourceARN: aws.String(r.arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: cloudwatch_event_rule %s: list tags (region=%s): %v\n", r.name, region, err)
					return nil
				}
				tags := make(map[string]string, len(tagsOut.Tags))
				for _, t := range tagsOut.Tags {
					tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
				}
				r.tags = tags
				mu.Lock()
				ok = append(ok, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool { return ok[i].name < ok[j].name })

		for _, r := range ok {
			if !MatchesAll(r.tags, args.TagSelectors) {
				continue
			}
			native := map[string]string{
				"rule_name":      r.name,
				"arn":            r.arn,
				"event_bus_name": r.bus,
			}
			imps = append(imps, makeImportedResource(
				book,
				cloudwatchEventRuleTFType,
				r.name,
				r.name,
				region,
				args.AccountID,
				native,
				r.tags,
			))
			args.Emitter.ItemFound(slug, region, cloudwatchEventRuleTFType, r.name)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves an EventBridge rule by name on the default bus.
// Issues a single DescribeRule call to verify existence.
func (d *cloudwatchEventRuleDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	name, err := cloudwatchEventRuleNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.DescribeRule(ctx, &eventbridge.DescribeRuleInput{Name: aws.String(name)})
	if err != nil {
		var notFound *ebtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_cloudwatch_event_rule %q: %w", name, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeRule: %w", err)
	}
	bus := aws.ToString(out.EventBusName)
	if bus == "" {
		bus = defaultEventBusName
	}
	native := map[string]string{
		"rule_name":      name,
		"arn":            aws.ToString(out.Arn),
		"event_bus_name": bus,
	}
	return makeImportedResource(
		addressBook{},
		cloudwatchEventRuleTFType,
		name,
		name,
		region,
		accountID,
		native,
		nil,
	), nil
}

// cloudwatchEventRuleNameFromID extracts a rule name from an import ID.
// Accepts a bare rule name (default bus) or "<bus>/<name>" (custom bus,
// where the rule name is the part after the slash). ARN shapes,
// multi-segment paths, and whitespace return ErrNotSupported.
func cloudwatchEventRuleNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("cloudwatch_event_rule: empty id: %w", ErrNotSupported)
	}
	// Reject ARN-shaped strings up front. EventBridge ARNs are
	// arn:aws:events:<region>:<account>:rule/<name> (or with a bus
	// segment) — accepting them via the "/<name>" branch below would
	// silently succeed on the wrong shape.
	if strings.HasPrefix(id, "arn:") {
		return "", fmt.Errorf("cloudwatch_event_rule: ARN-shaped id %q not supported: %w", id, ErrNotSupported)
	}
	if strings.Contains(id, "/") {
		parts := strings.Split(id, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", fmt.Errorf("cloudwatch_event_rule: id %q is not <bus>/<name>: %w", id, ErrNotSupported)
		}
		return parts[1], nil
	}
	if strings.ContainsAny(id, " :") {
		return "", fmt.Errorf("cloudwatch_event_rule: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
