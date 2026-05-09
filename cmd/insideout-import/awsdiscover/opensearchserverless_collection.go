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
	"github.com/aws/aws-sdk-go-v2/service/opensearchserverless"
	aosstypes "github.com/aws/aws-sdk-go-v2/service/opensearchserverless/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	oasCollectionTFType    = "aws_opensearchserverless_collection"
	oasCollectionAssetType = "aoss:collection"
)

// oasCollectionClient is the narrow subset of the opensearchserverless SDK
// the collection discoverer uses. ListCollections + ListTagsForResource for
// the per-region scan; BatchGetCollection for DiscoverByID.
type oasCollectionClient interface {
	ListCollections(ctx context.Context, in *opensearchserverless.ListCollectionsInput, opts ...func(*opensearchserverless.Options)) (*opensearchserverless.ListCollectionsOutput, error)
	ListTagsForResource(ctx context.Context, in *opensearchserverless.ListTagsForResourceInput, opts ...func(*opensearchserverless.Options)) (*opensearchserverless.ListTagsForResourceOutput, error)
	BatchGetCollection(ctx context.Context, in *opensearchserverless.BatchGetCollectionInput, opts ...func(*opensearchserverless.Options)) (*opensearchserverless.BatchGetCollectionOutput, error)
}

type oasCollectionDiscoverer struct {
	new            func(region string) oasCollectionClient
	maxConcurrency int
}

func newOpenSearchServerlessCollectionDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &oasCollectionDiscoverer{
		new: func(region string) oasCollectionClient {
			return opensearchserverless.NewFromConfig(cfg, func(o *opensearchserverless.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *oasCollectionDiscoverer) ResourceType() string { return oasCollectionTFType }

// Discover paginates ListCollections and filters by name prefix matching
// project. AOSS has no server-side filter on collection name, so this is
// the cheapest correct shape. Per-collection tag fetches fan out under a
// bounded errgroup.
//
// Multi-region (#291): outer loop walks args.Regions building a per-region
// SDK client. The legacy "Project=<project>" tag check is preserved as a
// back-compat implicit filter when args.Project is non-empty (composer-
// emitted stacks rely on it). Operator selectors AND on top.
//
// Import ID for aws_opensearchserverless_collection is the collection ID
// (e.g. "54twtpfw8h10ppzax9ad").
func (d *oasCollectionDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "opensearchserverless_collection"
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		type entry struct {
			id   string
			name string
			arn  string
			tags map[string]string
		}
		var candidates []entry

		input := &opensearchserverless.ListCollectionsInput{}
		for {
			out, err := client.ListCollections(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListCollections (region=%s): %w", region, err)
			}
			for i := range out.CollectionSummaries {
				c := &out.CollectionSummaries[i]
				name := aws.ToString(c.Name)
				if args.Project != "" && !strings.HasPrefix(name, args.Project) {
					continue
				}
				candidates = append(candidates, entry{
					id:   aws.ToString(c.Id),
					name: name,
					arn:  aws.ToString(c.Arn),
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		// Per-collection ListTagsForResource fan-out under bounded errgroup.
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
		for _, e := range candidates {
			e := e
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				if e.arn == "" {
					mu.Lock()
					ok = append(ok, entry{id: e.id, name: e.name, arn: e.arn, tags: map[string]string{}})
					mu.Unlock()
					return nil
				}
				tagsOut, err := client.ListTagsForResource(gctx, &opensearchserverless.ListTagsForResourceInput{ResourceArn: aws.String(e.arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: opensearchserverless_collection %s: list tags (region=%s): %v\n", e.name, region, err)
					return nil
				}
				tags := make(map[string]string, len(tagsOut.Tags))
				for _, t := range tagsOut.Tags {
					tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
				}
				mu.Lock()
				ok = append(ok, entry{id: e.id, name: e.name, arn: e.arn, tags: tags})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool { return ok[i].id < ok[j].id })

		for _, e := range ok {
			// Legacy Project=<project> back-compat filter.
			if args.Project != "" && e.tags["Project"] != args.Project {
				continue
			}
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			native := map[string]string{
				"collection_id": e.id,
				"name":          e.name,
				"arn":           e.arn,
			}
			imps = append(imps, makeImportedResource(
				book,
				oasCollectionTFType,
				e.name,
				e.id,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(slug, region, oasCollectionTFType, e.id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves an OpenSearch Serverless collection by ID
// (e.g. "54twtpfw8h10ppzax9ad") or ARN
// (arn:aws:aoss:<region>:<account>:collection/<id>). Issues a single
// BatchGetCollection call to verify existence.
func (d *oasCollectionDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	collectionID, err := oasCollectionIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.BatchGetCollection(ctx, &opensearchserverless.BatchGetCollectionInput{Ids: []string{collectionID}})
	if err != nil {
		var notFound *aosstypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_opensearchserverless_collection %q: %w", collectionID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("BatchGetCollection: %w", err)
	}
	if len(out.CollectionDetails) == 0 {
		return imported.ImportedResource{}, fmt.Errorf("aws_opensearchserverless_collection %q: %w", collectionID, ErrNotFound)
	}
	c := &out.CollectionDetails[0]
	name := aws.ToString(c.Name)
	arn := aws.ToString(c.Arn)
	native := map[string]string{
		"collection_id": collectionID,
		"name":          name,
		"arn":           arn,
	}
	return makeImportedResource(
		addressBook{},
		oasCollectionTFType,
		name,
		collectionID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// oasCollectionIDFromID extracts a bare collection ID from one of the
// accepted shapes: a bare collection ID, or an ARN of the form
// arn:aws:aoss:<region>:<account>:collection/<id>. Anything else returns
// ErrNotSupported so dep-chase routes it to its unresolvable bucket.
func oasCollectionIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("opensearchserverless_collection: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("opensearchserverless_collection: parse arn: %w", err)
		}
		if parsed.Service != "aoss" {
			return "", fmt.Errorf("opensearchserverless_collection: not an aoss arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// Resource is "collection/<id>".
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "collection" || parts[1] == "" {
			return "", fmt.Errorf("opensearchserverless_collection: arn resource %q is not collection/<id>: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("opensearchserverless_collection: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
