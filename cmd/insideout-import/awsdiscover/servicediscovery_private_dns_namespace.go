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
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
	sdtypes "github.com/aws/aws-sdk-go-v2/service/servicediscovery/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Bucket C, hand-rolled (#466 follow-up): Cloud Control returns
// UnsupportedActionException on READ for
// AWS::ServiceDiscovery::PrivateDnsNamespace, so neither the unified
// cloudControlDiscoverer nor SDKLister can resolve a namespace's
// properties. Native servicediscovery SDK end-to-end is the only path.
//
// Terraform's import id is "<namespace_id>:<vpc_id>" per the v6.x
// provider docs
// (website/docs/r/service_discovery_private_dns_namespace.html.markdown).
// The servicediscovery SDK does NOT surface VpcId on either the
// ListNamespaces summary or the GetNamespace response — the field is
// captured at namespace-create time but only retrievable via the
// associated Route53 private hosted zone, whose ID lives in
// Namespace.Properties.DnsProperties.HostedZoneId. We chase the VPC ID
// via Route53 GetHostedZone (one extra call per namespace) so the
// emitted import id matches the terraform provider's expected shape.
const (
	serviceDiscoveryPrivateDNSNamespaceTFType = "aws_service_discovery_private_dns_namespace"
	serviceDiscoveryPrivateDNSNamespaceSlug   = "service_discovery_private_dns_namespace"
	// vpcIDPlaceholderUnknown is emitted when Route53 GetHostedZone
	// returns no VPC associations for a private namespace's hosted
	// zone — surfaces a structurally-valid but operator-unactionable
	// import id rather than silently dropping the row. The discoverer
	// also issues a ServiceWarn alongside so the operator sees why the
	// resulting `terraform import` will fail.
	vpcIDPlaceholderUnknown = "UNKNOWN"
)

// serviceDiscoveryPrivateDNSNamespaceClient is the narrow subset of the
// Cloud Map (servicediscovery) SDK the discoverer uses. ListNamespaces
// supports a server-side TYPE=DNS_PRIVATE filter, so we never see
// HTTP or public-DNS namespaces in the response — no client-side type
// filter needed.
type serviceDiscoveryPrivateDNSNamespaceClient interface {
	ListNamespaces(ctx context.Context, in *servicediscovery.ListNamespacesInput, opts ...func(*servicediscovery.Options)) (*servicediscovery.ListNamespacesOutput, error)
	GetNamespace(ctx context.Context, in *servicediscovery.GetNamespaceInput, opts ...func(*servicediscovery.Options)) (*servicediscovery.GetNamespaceOutput, error)
	ListTagsForResource(ctx context.Context, in *servicediscovery.ListTagsForResourceInput, opts ...func(*servicediscovery.Options)) (*servicediscovery.ListTagsForResourceOutput, error)
}

// route53HostedZoneClient is the narrow subset of the Route53 SDK we
// use to recover the VPC ID associated with a private DNS namespace's
// hosted zone. Route53 is a global API — the same client serves every
// AWS region.
type route53HostedZoneClient interface {
	GetHostedZone(ctx context.Context, in *route53.GetHostedZoneInput, opts ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
}

type serviceDiscoveryPrivateDNSNamespaceDiscoverer struct {
	newSD          func(region string) serviceDiscoveryPrivateDNSNamespaceClient
	newR53         func() route53HostedZoneClient
	maxConcurrency int
}

func newServiceDiscoveryPrivateDNSNamespaceDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &serviceDiscoveryPrivateDNSNamespaceDiscoverer{
		newSD: func(region string) serviceDiscoveryPrivateDNSNamespaceClient {
			return servicediscovery.NewFromConfig(cfg, func(o *servicediscovery.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		newR53: func() route53HostedZoneClient {
			return route53.NewFromConfig(cfg)
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *serviceDiscoveryPrivateDNSNamespaceDiscoverer) ResourceType() string {
	return serviceDiscoveryPrivateDNSNamespaceTFType
}

// Discover paginates ListNamespaces with a TYPE=DNS_PRIVATE filter per
// region, then fans out per-namespace under a bounded errgroup to:
//
//   - Fetch tags via ListTagsForResource(ResourceARN=ns.Arn).
//   - Recover the VPC ID by calling Route53 GetHostedZone on the
//     namespace's HostedZoneId (extracted from Namespace.Properties
//     via GetNamespace, since ListNamespaces' summary does NOT carry
//     the hosted zone reliably).
//
// Tag-based project filter: the legacy Project=<project> back-compat
// check + operator-supplied TagSelectors AND on top. Mirrors the
// bedrock_guardrail posture.
//
// Failure modes inside the per-namespace fan-out are fail-open at the
// item level: a tag-fetch error skips that one namespace with a stderr
// warn rather than aborting the region; a Route53 hop error emits the
// namespace with vpc_id="UNKNOWN" and surfaces a ServiceWarn so the
// operator sees the gap. ListNamespaces / GetNamespace failures at the
// region level abort that region and surface as the outer error.
func (d *serviceDiscoveryPrivateDNSNamespaceDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = serviceDiscoveryPrivateDNSNamespaceSlug
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.newSD(region)
		r53 := d.newR53()

		type summary struct {
			id   string
			arn  string
			name string
		}
		var candidates []summary

		input := &servicediscovery.ListNamespacesInput{
			Filters: []sdtypes.NamespaceFilter{{
				Name:   sdtypes.NamespaceFilterNameType,
				Values: []string{string(sdtypes.NamespaceTypeDnsPrivate)},
			}},
		}
		for {
			out, err := client.ListNamespaces(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListNamespaces (region=%s): %w", region, err)
			}
			for i := range out.Namespaces {
				n := &out.Namespaces[i]
				// Server-side TYPE filter already pins DNS_PRIVATE, but
				// belt-and-braces against an API regression: only
				// surface DNS_PRIVATE rows.
				if n.Type != sdtypes.NamespaceTypeDnsPrivate {
					continue
				}
				name := aws.ToString(n.Name)
				if args.Project != "" && !strings.HasPrefix(name, args.Project) {
					continue
				}
				candidates = append(candidates, summary{
					id:   aws.ToString(n.Id),
					arn:  aws.ToString(n.Arn),
					name: name,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		type resolved struct {
			id           string
			arn          string
			name         string
			hostedZoneID string
			vpcID        string
			tags         map[string]string
			vpcWarn      string // non-empty when VPC resolution fell back to UNKNOWN
		}
		var (
			mu sync.Mutex
			ok []resolved
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, c := range candidates {
			c := c
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				// Step 1: GetNamespace to recover HostedZoneId. The
				// ListNamespaces summary does not always populate
				// Properties on every region/version combo, so we
				// always re-fetch.
				gnOut, err := client.GetNamespace(gctx, &servicediscovery.GetNamespaceInput{Id: aws.String(c.id)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					// One-namespace GetNamespace failure: warn +
					// skip this namespace, do not abort the region.
					var nf *sdtypes.NamespaceNotFound
					if !errors.As(err, &nf) {
						fmt.Fprintf(os.Stderr, "discover: WARN: service_discovery_private_dns_namespace %s: GetNamespace (region=%s): %v\n", c.name, region, err)
					}
					return nil
				}
				ns := gnOut.Namespace
				if ns == nil {
					return nil
				}
				hostedZoneID := ""
				if ns.Properties != nil && ns.Properties.DnsProperties != nil {
					hostedZoneID = aws.ToString(ns.Properties.DnsProperties.HostedZoneId)
				}

				// Step 2: ListTagsForResource for this namespace ARN.
				var tags map[string]string
				if c.arn != "" {
					tagsOut, err := client.ListTagsForResource(gctx, &servicediscovery.ListTagsForResourceInput{ResourceARN: aws.String(c.arn)})
					if err != nil {
						if cerr := gctx.Err(); cerr != nil {
							return cerr
						}
						fmt.Fprintf(os.Stderr, "discover: WARN: service_discovery_private_dns_namespace %s: ListTagsForResource (region=%s): %v\n", c.name, region, err)
						// Skip this namespace — tag filter cannot
						// be evaluated without tags.
						return nil
					}
					tags = make(map[string]string, len(tagsOut.Tags))
					for _, t := range tagsOut.Tags {
						tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
					}
				} else {
					tags = map[string]string{}
				}

				// Step 3: Route53 GetHostedZone to recover the VPC
				// ID. Private hosted zones can have multiple VPC
				// associations; the TF resource only stores one
				// (the "primary" used at create time). Pick the
				// first VPC entry deterministically — alphabetic by
				// VPCId — so re-discovery doesn't churn the import
				// id on accounts with multi-VPC associations.
				vpcID := vpcIDPlaceholderUnknown
				warn := ""
				if hostedZoneID != "" {
					hzOut, err := r53.GetHostedZone(gctx, &route53.GetHostedZoneInput{Id: aws.String(hostedZoneID)})
					if err != nil {
						if cerr := gctx.Err(); cerr != nil {
							return cerr
						}
						warn = fmt.Sprintf("Route53 GetHostedZone for namespace %s (zone=%s): %v", c.name, hostedZoneID, err)
					} else if len(hzOut.VPCs) > 0 {
						ids := make([]string, 0, len(hzOut.VPCs))
						for _, v := range hzOut.VPCs {
							if id := aws.ToString(v.VPCId); id != "" {
								ids = append(ids, id)
							}
						}
						if len(ids) > 0 {
							sort.Strings(ids)
							vpcID = ids[0]
						} else {
							warn = fmt.Sprintf("Route53 GetHostedZone for namespace %s (zone=%s): no VPC associations on response", c.name, hostedZoneID)
						}
					} else {
						warn = fmt.Sprintf("Route53 GetHostedZone for namespace %s (zone=%s): empty VPCs slice", c.name, hostedZoneID)
					}
				} else {
					warn = fmt.Sprintf("namespace %s has no DnsProperties.HostedZoneId; cannot resolve VPC", c.name)
				}

				mu.Lock()
				ok = append(ok, resolved{
					id:           c.id,
					arn:          c.arn,
					name:         c.name,
					hostedZoneID: hostedZoneID,
					vpcID:        vpcID,
					tags:         tags,
					vpcWarn:      warn,
				})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, err
		}

		sort.Slice(ok, func(i, j int) bool { return ok[i].id < ok[j].id })

		for _, r := range ok {
			// Legacy Project=<project> back-compat filter (matches
			// bedrock_guardrail / apigatewayv2_stage posture).
			if args.Project != "" && r.tags["Project"] != args.Project {
				continue
			}
			if !MatchesAll(r.tags, args.TagSelectors) {
				continue
			}
			if r.vpcWarn != "" {
				args.Emitter.ServiceWarn(slug, region, r.vpcWarn)
			}
			importID := r.id + ":" + r.vpcID
			native := map[string]string{
				"namespace_id": r.id,
				"arn":          r.arn,
				"vpc_id":       r.vpcID,
			}
			if r.hostedZoneID != "" {
				native["hosted_zone_id"] = r.hostedZoneID
			}
			imps = append(imps, makeImportedResource(
				book,
				serviceDiscoveryPrivateDNSNamespaceTFType,
				r.name,
				importID,
				region,
				args.AccountID,
				native,
				r.tags,
			))
			args.Emitter.ItemFound(slug, region, serviceDiscoveryPrivateDNSNamespaceTFType, importID)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves a private DNS namespace by its terraform import
// id "<namespace_id>:<vpc_id>" or a bare namespace id. Issues a single
// GetNamespace call to verify existence; tags are not refetched (dep-
// chase doesn't need them).
func (d *serviceDiscoveryPrivateDNSNamespaceDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	nsID, vpcID, err := serviceDiscoveryPrivateDNSNamespaceIDParts(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.newSD(region)
	out, err := client.GetNamespace(ctx, &servicediscovery.GetNamespaceInput{Id: aws.String(nsID)})
	if err != nil {
		var nf *sdtypes.NamespaceNotFound
		if errors.As(err, &nf) {
			return imported.ImportedResource{}, fmt.Errorf("aws_service_discovery_private_dns_namespace %q: %w", nsID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetNamespace: %w", err)
	}
	ns := out.Namespace
	if ns == nil {
		return imported.ImportedResource{}, fmt.Errorf("aws_service_discovery_private_dns_namespace %q: %w", nsID, ErrNotFound)
	}
	if ns.Type != sdtypes.NamespaceTypeDnsPrivate {
		return imported.ImportedResource{}, fmt.Errorf("namespace %q is not DNS_PRIVATE (got %q): %w", nsID, string(ns.Type), ErrNotSupported)
	}
	hostedZoneID := ""
	if ns.Properties != nil && ns.Properties.DnsProperties != nil {
		hostedZoneID = aws.ToString(ns.Properties.DnsProperties.HostedZoneId)
	}
	// Best-effort VPC resolution when the caller didn't provide one.
	if vpcID == "" {
		vpcID = vpcIDPlaceholderUnknown
		if hostedZoneID != "" {
			r53 := d.newR53()
			hzOut, err := r53.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(hostedZoneID)})
			if err == nil && len(hzOut.VPCs) > 0 {
				ids := make([]string, 0, len(hzOut.VPCs))
				for _, v := range hzOut.VPCs {
					if id := aws.ToString(v.VPCId); id != "" {
						ids = append(ids, id)
					}
				}
				if len(ids) > 0 {
					sort.Strings(ids)
					vpcID = ids[0]
				}
			}
		}
	}
	name := aws.ToString(ns.Name)
	importID := nsID + ":" + vpcID
	native := map[string]string{
		"namespace_id": nsID,
		"arn":          aws.ToString(ns.Arn),
		"vpc_id":       vpcID,
	}
	if hostedZoneID != "" {
		native["hosted_zone_id"] = hostedZoneID
	}
	return makeImportedResource(
		addressBook{},
		serviceDiscoveryPrivateDNSNamespaceTFType,
		name,
		importID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// serviceDiscoveryPrivateDNSNamespaceIDParts splits an import id of the
// form "<namespace_id>:<vpc_id>" into its two parts. A bare namespace
// id (no colon) returns (id, "") so DiscoverByID can fall back to
// Route53 VPC resolution. Anything that contains a slash, space, or
// more than one colon is rejected with ErrNotSupported.
func serviceDiscoveryPrivateDNSNamespaceIDParts(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("service_discovery_private_dns_namespace: empty id: %w", ErrNotSupported)
	}
	if strings.ContainsAny(id, " /,") {
		return "", "", fmt.Errorf("service_discovery_private_dns_namespace: unrecognized id %q: %w", id, ErrNotSupported)
	}
	if !strings.Contains(id, ":") {
		return id, "", nil
	}
	parts := strings.Split(id, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("service_discovery_private_dns_namespace: id %q is not <namespace_id>:<vpc_id>: %w", id, ErrNotSupported)
	}
	return parts[0], parts[1], nil
}
