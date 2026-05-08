package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	route53ZoneTFType    = "aws_route53_zone"
	route53ZoneAssetType = "route53:hostedzone"
)

// route53HostedZonePrefix is the leading path segment Route 53 returns
// inside HostedZone.Id (e.g. "/hostedzone/Z092…"). The Terraform import
// ID is the bare zone ID, so we strip this on the way in.
const route53HostedZonePrefix = "/hostedzone/"

// route53ZoneClient is the narrow subset of the Route 53 SDK the
// hosted-zone discoverer uses. Mirrors the per-service interface
// pattern used everywhere else in this package so tests can mock the
// SDK boundary without depending on real AWS credentials.
type route53ZoneClient interface {
	ListHostedZones(ctx context.Context, in *route53.ListHostedZonesInput, opts ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error)
	GetHostedZone(ctx context.Context, in *route53.GetHostedZoneInput, opts ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
	ListTagsForResource(ctx context.Context, in *route53.ListTagsForResourceInput, opts ...func(*route53.Options)) (*route53.ListTagsForResourceOutput, error)
}

type route53ZoneDiscoverer struct {
	new func(region string) route53ZoneClient
}

func newRoute53ZoneDiscoverer(cfg aws.Config) Discoverer {
	return &route53ZoneDiscoverer{new: func(region string) route53ZoneClient {
		return route53.NewFromConfig(cfg, func(o *route53.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *route53ZoneDiscoverer) ResourceType() string { return route53ZoneTFType }

// Discover paginates ListHostedZones and filters by name prefix
// matching project. Route 53 has no server-side filter on
// ListHostedZones, but InsideOut convention is to embed the project
// prefix in the zone name so client-side prefix filtering matches the
// bounded-account assumption already used by the IAM-role discoverer.
//
// Route 53 is account-global — args.Regions is ignored. The
// Identity.Region stamp is left empty for hosted zones to reflect that.
// Per-zone ListTagsForResource fetches the tag map for tag-selector
// post-filtering and tag persistence onto Identity.Tags.
//
// Import ID for aws_route53_zone is the hosted zone ID with the
// "/hostedzone/" path prefix stripped (the Terraform provider rejects
// the prefixed form on import).
func (d *route53ZoneDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	const slug = "route53_zone"
	// Route 53 is account-global; emit a single (svc,"") scope per run.
	// Empty region in the event matches the empty Identity.Region the
	// per-zone stamp uses.
	regionStart := time.Now()
	args.Emitter.ServiceStart(slug, "")
	regionCount := 0
	client := d.new("")

	type zone struct {
		id          string // bare ID, prefix stripped
		name        string // DNS name, trailing dot stripped
		privateZone bool
	}
	var zones []zone

	input := &route53.ListHostedZonesInput{}
	for {
		out, err := client.ListHostedZones(ctx, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListHostedZones: %w", err)
		}
		for i := range out.HostedZones {
			hz := &out.HostedZones[i]
			id := strings.TrimPrefix(aws.ToString(hz.Id), route53HostedZonePrefix)
			name := strings.TrimSuffix(aws.ToString(hz.Name), ".")
			if args.Project != "" && !strings.HasPrefix(name, args.Project) {
				continue
			}
			z := zone{id: id, name: name}
			if hz.Config != nil {
				z.privateZone = hz.Config.PrivateZone
			}
			zones = append(zones, z)
		}
		if out.IsTruncated {
			input.Marker = out.NextMarker
			continue
		}
		break
	}

	sort.Slice(zones, func(i, j int) bool { return zones[i].id < zones[j].id })

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(zones))
	for _, z := range zones {
		tags, err := fetchRoute53ZoneTags(ctx, client, z.id)
		if err != nil {
			args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (zone=%s): %w", z.id, err)
		}
		if !MatchesAll(tags, args.TagSelectors) {
			continue
		}
		native := map[string]string{
			"hosted_zone_id": z.id,
			"name":           z.name,
		}
		if z.privateZone {
			native["private_zone"] = "true"
		}
		imps = append(imps, makeImportedResource(
			book,
			route53ZoneTFType,
			z.name,
			z.id,
			"", // Route 53 is global; do not stamp a region.
			args.AccountID,
			native,
			tags,
		))
		args.Emitter.ItemFound(slug, "", route53ZoneTFType, z.id)
		regionCount++
	}
	args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
	return imps, nil
}

// fetchRoute53ZoneTags returns the hosted zone's tag map. The Route 53
// ListTagsForResource response groups tags inside a ResourceTagSet
// rather than returning a flat list — convert to a string-keyed map.
// Empty (non-nil) map for "fetched, but the zone has no tags."
func fetchRoute53ZoneTags(ctx context.Context, client route53ZoneClient, zoneID string) (map[string]string, error) {
	out, err := client.ListTagsForResource(ctx, &route53.ListTagsForResourceInput{
		ResourceType: route53types.TagResourceTypeHostedzone,
		ResourceId:   aws.String(zoneID),
	})
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	if out.ResourceTagSet != nil {
		for _, t := range out.ResourceTagSet.Tags {
			tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
	}
	return tags, nil
}

// DiscoverByID resolves a hosted zone by zone ID. Accepts either the
// bare ID (Z092…) or the path-prefixed form (/hostedzone/Z092…) the SDK
// returns from ListHostedZones. Issues a single GetHostedZone call to
// verify existence.
func (d *route53ZoneDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	zoneID, err := route53ZoneIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(zoneID)})
	if err != nil {
		var notFound *route53types.NoSuchHostedZone
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_route53_zone %q: %w", zoneID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetHostedZone: %w", err)
	}
	if out.HostedZone == nil {
		return imported.ImportedResource{}, fmt.Errorf("aws_route53_zone %q: %w", zoneID, ErrNotFound)
	}
	hz := out.HostedZone
	name := strings.TrimSuffix(aws.ToString(hz.Name), ".")
	native := map[string]string{
		"hosted_zone_id": zoneID,
		"name":           name,
	}
	if hz.Config != nil && hz.Config.PrivateZone {
		native["private_zone"] = "true"
	}
	return makeImportedResource(
		addressBook{},
		route53ZoneTFType,
		name,
		zoneID,
		"", // Route 53 is global; do not stamp a region.
		accountID,
		native,
		nil,
	), nil
}

// route53ZoneIDFromID extracts a bare hosted-zone ID from one of two
// accepted shapes: a bare ID (Z092260438L3LX7TNQKK2) or the
// path-prefixed form Route 53 itself returns
// (/hostedzone/Z092260438L3LX7TNQKK2). Anything else returns
// ErrNotSupported so dep-chase routes it to the unresolvable bucket.
//
// Route 53 hosted zones do not have a stable ARN format consumed by
// `terraform import`, so an ARN is not an accepted shape here.
func route53ZoneIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("route53_zone: empty id: %w", ErrNotSupported)
	}
	if strings.HasPrefix(id, route53HostedZonePrefix) {
		bare := strings.TrimPrefix(id, route53HostedZonePrefix)
		if bare == "" || strings.ContainsAny(bare, " /:") {
			return "", fmt.Errorf("route53_zone: unrecognized id %q: %w", id, ErrNotSupported)
		}
		return bare, nil
	}
	// Bare zone IDs are alphanumeric with no path or whitespace; reject
	// anything else (including ARN-shaped strings) as unsupported.
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("route53_zone: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
