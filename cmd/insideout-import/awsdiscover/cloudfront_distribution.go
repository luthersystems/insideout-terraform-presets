package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	cloudfrontDistributionTFType    = "aws_cloudfront_distribution"
	cloudfrontDistributionAssetType = "cloudfront:distribution"
)

// cloudfrontDistributionClient is the narrow subset of the CloudFront
// SDK the distribution discoverer uses. Mirrors the per-service
// interface pattern used everywhere else in this package so tests can
// mock the SDK boundary without depending on real AWS credentials.
type cloudfrontDistributionClient interface {
	ListDistributions(ctx context.Context, in *cloudfront.ListDistributionsInput, opts ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error)
	GetDistribution(ctx context.Context, in *cloudfront.GetDistributionInput, opts ...func(*cloudfront.Options)) (*cloudfront.GetDistributionOutput, error)
	ListTagsForResource(ctx context.Context, in *cloudfront.ListTagsForResourceInput, opts ...func(*cloudfront.Options)) (*cloudfront.ListTagsForResourceOutput, error)
}

type cloudfrontDistributionDiscoverer struct {
	new func(region string) cloudfrontDistributionClient
}

func newCloudFrontDistributionDiscoverer(cfg aws.Config) Discoverer {
	return &cloudfrontDistributionDiscoverer{new: func(region string) cloudfrontDistributionClient {
		return cloudfront.NewFromConfig(cfg, func(o *cloudfront.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *cloudfrontDistributionDiscoverer) ResourceType() string {
	return cloudfrontDistributionTFType
}

// Discover paginates ListDistributions and filters by Comment-prefix
// matching project. CloudFront has no server-side filter on
// ListDistributions, but InsideOut convention is to stamp the project
// prefix into the distribution Comment field (CloudFront's
// "name-equivalent") so client-side prefix filtering matches the
// bounded-account assumption already used by the IAM-role discoverer.
//
// CloudFront is account-global — args.Regions is ignored. The
// Identity.Region stamp is left empty for distributions to reflect
// that. Per-distribution ListTagsForResource fetches the tag map for
// tag-selector post-filtering and tag persistence onto Identity.Tags.
//
// Import ID for aws_cloudfront_distribution is the distribution ID
// (e.g. E2TKCBW0F18ZRW).
func (d *cloudfrontDistributionDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	const slug = "cloudfront_distribution"
	// CloudFront is account-global; emit a single (svc,"") scope per
	// run. Empty region in the event matches the empty Identity.Region
	// the per-distribution stamp uses.
	regionStart := time.Now()
	args.Emitter.ServiceStart(slug, "")
	regionCount := 0
	client := d.new("")

	type dist struct {
		id         string
		arn        string
		comment    string
		domainName string
		aliases    []string
	}
	var dists []dist

	input := &cloudfront.ListDistributionsInput{}
	for {
		out, err := client.ListDistributions(ctx, input)
		if err != nil {
			args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListDistributions: %w", err)
		}
		if out.DistributionList == nil {
			break
		}
		dl := out.DistributionList
		for i := range dl.Items {
			s := &dl.Items[i]
			comment := aws.ToString(s.Comment)
			if args.Project != "" && !strings.HasPrefix(comment, args.Project) {
				continue
			}
			x := dist{
				id:         aws.ToString(s.Id),
				arn:        aws.ToString(s.ARN),
				comment:    comment,
				domainName: aws.ToString(s.DomainName),
			}
			if s.Aliases != nil {
				x.aliases = append(x.aliases, s.Aliases.Items...)
			}
			dists = append(dists, x)
		}
		if aws.ToBool(dl.IsTruncated) {
			input.Marker = dl.NextMarker
			continue
		}
		break
	}

	sort.Slice(dists, func(i, j int) bool { return dists[i].id < dists[j].id })

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(dists))
	for _, x := range dists {
		tags, err := fetchCloudFrontDistributionTags(ctx, client, x.arn)
		if err != nil {
			args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (distribution=%s): %w", x.id, err)
		}
		if !MatchesAll(tags, args.TagSelectors) {
			continue
		}
		// CloudFront's "name-equivalent" is the Comment field; fall
		// back to the distribution ID when the operator left it blank
		// so NameHint always carries something readable.
		name := x.comment
		if name == "" {
			name = x.id
		}
		native := map[string]string{
			"distribution_id": x.id,
			"arn":             x.arn,
			"domain_name":     x.domainName,
		}
		if len(x.aliases) > 0 {
			native["primary_alias"] = x.aliases[0]
		}
		imps = append(imps, makeImportedResource(
			book,
			cloudfrontDistributionTFType,
			name,
			x.id,
			"", // CloudFront is global; do not stamp a region.
			args.AccountID,
			native,
			tags,
		))
		args.Emitter.ItemFound(slug, "", cloudfrontDistributionTFType, x.id)
		regionCount++
	}
	args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
	return imps, nil
}

// fetchCloudFrontDistributionTags returns the distribution's tag map.
// CloudFront groups tags inside a Tags shape with an Items slice rather
// than returning a flat list — convert to a string-keyed map. Empty
// (non-nil) map for "fetched, but the distribution has no tags."
func fetchCloudFrontDistributionTags(ctx context.Context, client cloudfrontDistributionClient, arn string) (map[string]string, error) {
	out, err := client.ListTagsForResource(ctx, &cloudfront.ListTagsForResourceInput{Resource: aws.String(arn)})
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	if out.Tags != nil {
		for _, t := range out.Tags.Items {
			tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
	}
	return tags, nil
}

// DiscoverByID resolves a distribution by ID or ARN. Accepts the bare
// distribution ID (E1U5RQF7T870K0) or a CloudFront ARN
// (arn:aws:cloudfront::<account>:distribution/<id>). Issues a single
// GetDistribution call to verify existence.
func (d *cloudfrontDistributionDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	distID, err := cloudfrontDistributionIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(distID)})
	if err != nil {
		var notFound *cftypes.NoSuchDistribution
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_cloudfront_distribution %q: %w", distID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetDistribution: %w", err)
	}
	if out.Distribution == nil {
		return imported.ImportedResource{}, fmt.Errorf("aws_cloudfront_distribution %q: %w", distID, ErrNotFound)
	}
	dst := out.Distribution
	arn := aws.ToString(dst.ARN)
	domain := aws.ToString(dst.DomainName)
	comment := ""
	if dst.DistributionConfig != nil {
		comment = aws.ToString(dst.DistributionConfig.Comment)
	}
	name := comment
	if name == "" {
		name = distID
	}
	native := map[string]string{
		"distribution_id": distID,
		"arn":             arn,
		"domain_name":     domain,
	}
	if dst.DistributionConfig != nil && dst.DistributionConfig.Aliases != nil &&
		len(dst.DistributionConfig.Aliases.Items) > 0 {
		native["primary_alias"] = dst.DistributionConfig.Aliases.Items[0]
	}
	return makeImportedResource(
		addressBook{},
		cloudfrontDistributionTFType,
		name,
		distID,
		"", // CloudFront is global; do not stamp a region.
		accountID,
		native,
		nil,
	), nil
}

// cloudfrontDistributionIDFromID extracts the distribution ID from one
// of two accepted inputs: the bare distribution ID (E1U5RQF7T870K0), or
// a CloudFront ARN
// (arn:aws:cloudfront::<account>:distribution/<id>). Anything else
// returns ErrNotSupported so dep-chase routes it to the unresolvable
// bucket.
func cloudfrontDistributionIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("cloudfront_distribution: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("cloudfront_distribution: parse arn: %w", err)
		}
		if parsed.Service != "cloudfront" {
			return "", fmt.Errorf("cloudfront_distribution: not a cloudfront arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "distribution" || parts[1] == "" {
			return "", fmt.Errorf("cloudfront_distribution: arn resource %q is not distribution/<id>: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("cloudfront_distribution: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
