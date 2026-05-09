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
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	eksPodIdentityTFType    = "aws_eks_pod_identity_association"
	eksPodIdentityAssetType = "eks:podidentityassociation"
)

// eksPodIdentityClient is the narrow subset of the EKS SDK the
// pod-identity-association discoverer uses. Mirrors the per-service
// interface pattern used everywhere else in this package so tests can
// mock the SDK boundary without depending on real AWS credentials.
type eksPodIdentityClient interface {
	ListClusters(ctx context.Context, in *eks.ListClustersInput, opts ...func(*eks.Options)) (*eks.ListClustersOutput, error)
	ListPodIdentityAssociations(ctx context.Context, in *eks.ListPodIdentityAssociationsInput, opts ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error)
	DescribePodIdentityAssociation(ctx context.Context, in *eks.DescribePodIdentityAssociationInput, opts ...func(*eks.Options)) (*eks.DescribePodIdentityAssociationOutput, error)
	ListTagsForResource(ctx context.Context, in *eks.ListTagsForResourceInput, opts ...func(*eks.Options)) (*eks.ListTagsForResourceOutput, error)
}

type eksPodIdentityDiscoverer struct {
	new            func(region string) eksPodIdentityClient
	maxConcurrency int
}

func newEKSPodIdentityDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &eksPodIdentityDiscoverer{
		new: func(region string) eksPodIdentityClient {
			return eks.NewFromConfig(cfg, func(o *eks.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *eksPodIdentityDiscoverer) ResourceType() string { return eksPodIdentityTFType }

// Discover walks each region, paginates ListClusters to enumerate the
// account's EKS clusters, filters by cluster-name prefix matching
// args.Project (associations themselves carry no project-aware name —
// the cluster name is the only place the InsideOut prefix lives), and
// then for each prefix-matching cluster paginates
// ListPodIdentityAssociations to enumerate that cluster's associations.
//
// Per-association ListTagsForResource fetches the association's tag map
// under a bounded errgroup so a many-cluster account doesn't serialize
// into a multi-minute wall-time. Per-item SDK errors are fail-closed
// (transient ListTagsForResource failures skip the association rather
// than aborting the run); ListClusters / ListPodIdentityAssociations
// errors abort the whole region. Parent-context cancellation is
// propagated via gctx.
//
// Import ID for aws_eks_pod_identity_association is
// "<cluster_name>,<association_id>" (the comma-separated form
// terraform-provider-aws expects on import).
func (d *eksPodIdentityDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	const slug = "eks_pod_identity"
	book := addressBook{}
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		// 1. Enumerate clusters in this region (paginated NextToken).
		var clusters []string
		clustersInput := &eks.ListClustersInput{}
		for {
			out, err := client.ListClusters(ctx, clustersInput)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListClusters (region=%s): %w", region, err)
			}
			for _, c := range out.Clusters {
				if args.Project == "" || strings.HasPrefix(c, args.Project) {
					clusters = append(clusters, c)
				}
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			clustersInput.NextToken = out.NextToken
		}
		sort.Strings(clusters)

		// 2. For each prefix-matching cluster, enumerate its
		// PodIdentityAssociations (paginated NextToken).
		type assoc struct {
			cluster        string
			associationID  string
			associationArn string
			namespace      string
			serviceAccount string
			tags           map[string]string
		}
		var allAssocs []assoc
		for _, cluster := range clusters {
			input := &eks.ListPodIdentityAssociationsInput{ClusterName: aws.String(cluster)}
			for {
				out, err := client.ListPodIdentityAssociations(ctx, input)
				if err != nil {
					args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
					return nil, fmt.Errorf("ListPodIdentityAssociations (region=%s, cluster=%s): %w", region, cluster, err)
				}
				for _, a := range out.Associations {
					allAssocs = append(allAssocs, assoc{
						cluster:        aws.ToString(a.ClusterName),
						associationID:  aws.ToString(a.AssociationId),
						associationArn: aws.ToString(a.AssociationArn),
						namespace:      aws.ToString(a.Namespace),
						serviceAccount: aws.ToString(a.ServiceAccount),
					})
				}
				if out.NextToken == nil || *out.NextToken == "" {
					break
				}
				input.NextToken = out.NextToken
			}
		}

		// 3. Per-association tag fetch under bounded errgroup.
		var (
			mu sync.Mutex
			ok []assoc
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, a := range allAssocs {
			a := a
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				if a.associationArn == "" {
					// No ARN, no tag fetch. Keep the association
					// with nil tags so it's still surfaced.
					mu.Lock()
					ok = append(ok, a)
					mu.Unlock()
					return nil
				}
				tagsOut, err := client.ListTagsForResource(gctx, &eks.ListTagsForResourceInput{ResourceArn: aws.String(a.associationArn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: eks_pod_identity %s/%s: list tags (region=%s): %v\n", a.cluster, a.associationID, region, err)
					return nil
				}
				tags := tagsOut.Tags
				if tags == nil {
					tags = map[string]string{}
				}
				a.tags = tags
				mu.Lock()
				ok = append(ok, a)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool {
			if ok[i].cluster != ok[j].cluster {
				return ok[i].cluster < ok[j].cluster
			}
			return ok[i].associationID < ok[j].associationID
		})

		for _, a := range ok {
			if !MatchesAll(a.tags, args.TagSelectors) {
				continue
			}
			importID := a.cluster + "," + a.associationID
			nameHint := a.cluster + "/" + a.associationID
			native := map[string]string{
				"cluster_name":    a.cluster,
				"association_id":  a.associationID,
				"association_arn": a.associationArn,
				"namespace":       a.namespace,
				"service_account": a.serviceAccount,
			}
			imps = append(imps, makeImportedResource(
				book,
				eksPodIdentityTFType,
				nameHint,
				importID,
				region,
				args.AccountID,
				native,
				a.tags,
			))
			args.Emitter.ItemFound(slug, region, eksPodIdentityTFType, importID)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves an EKS pod-identity association by its
// "<cluster_name>,<association_id>" import ID. Issues a single
// DescribePodIdentityAssociation call to verify existence.
func (d *eksPodIdentityDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	cluster, assocID, err := eksPodIdentityIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.DescribePodIdentityAssociation(ctx, &eks.DescribePodIdentityAssociationInput{
		ClusterName:   aws.String(cluster),
		AssociationId: aws.String(assocID),
	})
	if err != nil {
		var notFound *ekstypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_eks_pod_identity_association %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribePodIdentityAssociation: %w", err)
	}
	if out.Association == nil {
		return imported.ImportedResource{}, fmt.Errorf("aws_eks_pod_identity_association %q: %w", id, ErrNotFound)
	}
	a := out.Association
	importID := cluster + "," + assocID
	nameHint := cluster + "/" + assocID
	native := map[string]string{
		"cluster_name":    cluster,
		"association_id":  assocID,
		"association_arn": aws.ToString(a.AssociationArn),
		"namespace":       aws.ToString(a.Namespace),
		"service_account": aws.ToString(a.ServiceAccount),
	}
	return makeImportedResource(
		addressBook{},
		eksPodIdentityTFType,
		nameHint,
		importID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// eksPodIdentityIDFromID parses a "<cluster_name>,<association_id>"
// import ID. Anything else (empty, no comma, multiple commas, empty
// segments) returns ErrNotSupported so dep-chase routes it to the
// unresolvable bucket.
func eksPodIdentityIDFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("eks_pod_identity: empty id: %w", ErrNotSupported)
	}
	parts := strings.Split(id, ",")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("eks_pod_identity: id %q is not <cluster>,<assoc_id>: %w", id, ErrNotSupported)
	}
	cluster := strings.TrimSpace(parts[0])
	assocID := strings.TrimSpace(parts[1])
	if cluster == "" || assocID == "" {
		return "", "", fmt.Errorf("eks_pod_identity: id %q has empty cluster or association id: %w", id, ErrNotSupported)
	}
	return cluster, assocID, nil
}
