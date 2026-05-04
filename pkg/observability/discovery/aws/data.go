// Data-tier AWS service inspectors: RDS, DynamoDB, ElastiCache,
// OpenSearch (managed + serverless union), MSK.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go (rds:412,
// dynamodb:902, elasticache:889, opensearch:1033, msk:938) plus the
// corresponding tag-filter helpers in aws_metrics.go
// (filterDynamoDBTablesByProjectTag:1323,
// filterElastiCacheCacheClustersByProjectTag:1723,
// filterElastiCacheReplicationGroupsByProjectTag:1746,
// filterElastiCacheByProjectTag:1775).
//
// The opensearch path is the trickiest piece: a single "opensearch"
// service in the registry covers BOTH managed-domain and AOSS serverless
// collections. discoverOpenSearchUnion fans out to both backends and
// merges the result so an AOSS-only deploy doesn't appear as zero
// resources (drift-detection bug #1018 in the InsideOut backend).

package aws

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearchserverless"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// --- RDS ---

func inspectRDS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := rds.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "describe-db-instances":
		out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
		if err != nil {
			return nil, err
		}
		if project != "" {
			return filter.Match(toSliceOfMaps(out.DBInstances), project, "TagList", filter.FormatKV), nil
		}
		return nilSliceToEmpty(out.DBInstances), nil
	case "describe-db-clusters":
		out, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{})
		if err != nil {
			return nil, err
		}
		if project != "" {
			return filter.Match(toSliceOfMaps(out.DBClusters), project, "TagList", filter.FormatKV), nil
		}
		return nilSliceToEmpty(out.DBClusters), nil
	case "get-metrics":
		return metricsRouted("rds")
	default:
		return nil, unsupportedActionError("rds", action)
	}
}

// --- DynamoDB ---

// dynamoDBTablesClient is the subset of the dynamodb SDK used by the
// shared filter helper. Mirrors the InsideOut backend's dynamoDBTablesClient
// (aws_metrics.go:1293).
type dynamoDBTablesClient interface {
	ListTables(ctx context.Context, params *dynamodb.ListTablesInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error)
	ListTagsOfResource(ctx context.Context, params *dynamodb.ListTagsOfResourceInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error)
}

// stsAccountClient resolves the caller's account id for ARN
// construction. DynamoDB's ListTagsOfResource needs an ARN but
// ListTables only returns names — we synthesize the ARN from
// arn:aws:dynamodb:<region>:<account>:table/<name>.
//
// Mirrors the InsideOut backend's stsAccountClient (aws_metrics.go:1301).
type stsAccountClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

func inspectDynamoDB(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-tables":
		return filterDynamoDBTablesByProjectTag(ctx, dynamodb.NewFromConfig(cfg), sts.NewFromConfig(cfg), cfg.Region, project)
	case "get-metrics":
		return metricsRouted("dynamodb")
	default:
		return nil, unsupportedActionError("dynamodb", action)
	}
}

// filterDynamoDBTablesByProjectTag paginates ListTables, resolves the
// account id once, then fans out ListTagsOfResource per table to keep
// only Project=<project>-tagged tables. Per-table errors log+skip;
// ListTables / GetCallerIdentity errors abort.
//
// TODO: derive partition from region (arn:aws-us-gov:, arn:aws-cn:) when
// GovCloud / China support arrives. Hardcoded to aws partition —
// commercial-only today, mirrors the InsideOut backend's stance.
//
// Mirrors the InsideOut backend's filterDynamoDBTablesByProjectTag (aws_metrics.go:1323).
func filterDynamoDBTablesByProjectTag(ctx context.Context, client dynamoDBTablesClient, stsClient stsAccountClient, region, project string) ([]string, error) {
	all := []string{}
	paginator := dynamodb.NewListTablesPaginator(client, &dynamodb.ListTablesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("dynamodb ListTables: %w", err)
		}
		all = append(all, page.TableNames...)
	}
	if project == "" {
		return all, nil
	}
	ident, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("sts GetCallerIdentity (for dynamodb arn construction): %w", err)
	}
	account := aws.ToString(ident.Account)
	matched := make([]string, 0, len(all))
	for _, name := range all {
		arn := fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, account, name)
		tagsOut, err := client.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
		if err != nil {
			log.Printf("[dynamodb ListTagsOfResource] skip table=%s: %v", name, err)
			continue
		}
		for _, t := range tagsOut.Tags {
			if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
				matched = append(matched, name)
				break
			}
		}
	}
	return matched, nil
}

// --- ElastiCache ---

// elasticacheClustersClient is the subset of the elasticache SDK used
// by the shared filter helpers. Mirrors the InsideOut backend's
// elasticacheClustersClient (aws_metrics.go:1698).
type elasticacheClustersClient interface {
	DescribeCacheClusters(ctx context.Context, params *elasticache.DescribeCacheClustersInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeCacheClustersOutput, error)
	DescribeReplicationGroups(ctx context.Context, params *elasticache.DescribeReplicationGroupsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReplicationGroupsOutput, error)
	ListTagsForResource(ctx context.Context, params *elasticache.ListTagsForResourceInput, optFns ...func(*elasticache.Options)) (*elasticache.ListTagsForResourceOutput, error)
}

func inspectElastiCache(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "describe-cache-clusters":
		return filterElastiCacheCacheClustersByProjectTag(ctx, elasticache.NewFromConfig(cfg), project)
	case "describe-replication-groups":
		return filterElastiCacheReplicationGroupsByProjectTag(ctx, elasticache.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("elasticache")
	default:
		return nil, unsupportedActionError("elasticache", action)
	}
}

func filterElastiCacheCacheClustersByProjectTag(ctx context.Context, client elasticacheClustersClient, project string) ([]elasticachetypes.CacheCluster, error) {
	return filterElastiCacheByProjectTag(
		ctx, client, project, "DescribeCacheClusters",
		func(ctx context.Context) ([]elasticachetypes.CacheCluster, error) {
			all := []elasticachetypes.CacheCluster{}
			p := elasticache.NewDescribeCacheClustersPaginator(client, &elasticache.DescribeCacheClustersInput{})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				all = append(all, page.CacheClusters...)
			}
			return all, nil
		},
		func(c elasticachetypes.CacheCluster) string { return aws.ToString(c.ARN) },
	)
}

func filterElastiCacheReplicationGroupsByProjectTag(ctx context.Context, client elasticacheClustersClient, project string) ([]elasticachetypes.ReplicationGroup, error) {
	return filterElastiCacheByProjectTag(
		ctx, client, project, "DescribeReplicationGroups",
		func(ctx context.Context) ([]elasticachetypes.ReplicationGroup, error) {
			all := []elasticachetypes.ReplicationGroup{}
			p := elasticache.NewDescribeReplicationGroupsPaginator(client, &elasticache.DescribeReplicationGroupsInput{})
			for p.HasMorePages() {
				page, err := p.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				all = append(all, page.ReplicationGroups...)
			}
			return all, nil
		},
		func(rg elasticachetypes.ReplicationGroup) string { return aws.ToString(rg.ARN) },
	)
}

// filterElastiCacheByProjectTag is the shared body for the two
// ElastiCache filter helpers. Generic over the SDK's typed list element;
// arnOf adapts the per-type ARN accessor; op gets interpolated into the
// log-grep prefix on paginator errors.
//
// Mirrors the InsideOut backend's filterElastiCacheByProjectTag (aws_metrics.go:1775).
func filterElastiCacheByProjectTag[T any](
	ctx context.Context,
	client elasticacheClustersClient,
	project, op string,
	listAll func(ctx context.Context) ([]T, error),
	arnOf func(T) string,
) ([]T, error) {
	all, err := listAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("elasticache %s: %w", op, err)
	}
	if project == "" {
		return all, nil
	}
	matched := make([]T, 0, len(all))
	for _, item := range all {
		arn := arnOf(item)
		if arn == "" {
			continue
		}
		tagsOut, err := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{
			ResourceName: aws.String(arn),
		})
		if err != nil {
			log.Printf("[elasticache ListTagsForResource] skip arn=%s: %v", arn, err)
			continue
		}
		for _, t := range tagsOut.TagList {
			if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
				matched = append(matched, item)
				break
			}
		}
	}
	return matched, nil
}

// --- OpenSearch (managed + AOSS union) ---

// opensearchAPI is the subset of the opensearch SDK used by the managed
// discovery helper. Mirrors the InsideOut backend's opensearchAPI (aws_inspect.go:1228).
type opensearchAPI interface {
	ListDomainNames(ctx context.Context, in *opensearch.ListDomainNamesInput, opts ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error)
	DescribeDomains(ctx context.Context, in *opensearch.DescribeDomainsInput, opts ...func(*opensearch.Options)) (*opensearch.DescribeDomainsOutput, error)
	ListTags(ctx context.Context, in *opensearch.ListTagsInput, opts ...func(*opensearch.Options)) (*opensearch.ListTagsOutput, error)
}

// aossAPI is the subset of the opensearchserverless SDK used by the AOSS
// discovery helper. Mirrors the InsideOut backend's aossAPI (aws_inspect.go:1234).
type aossAPI interface {
	ListCollections(ctx context.Context, in *opensearchserverless.ListCollectionsInput, opts ...func(*opensearchserverless.Options)) (*opensearchserverless.ListCollectionsOutput, error)
	ListTagsForResource(ctx context.Context, in *opensearchserverless.ListTagsForResourceInput, opts ...func(*opensearchserverless.Options)) (*opensearchserverless.ListTagsForResourceOutput, error)
}

func inspectOpenSearch(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-domains":
		client := opensearch.NewFromConfig(cfg)
		out, err := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
		if err != nil {
			return nil, err
		}
		return nilSliceToEmpty(out.DomainNames), nil
	case "describe-domains":
		// Union discovery: the `aws_opensearch` preset can deploy as
		// Managed OR Serverless on a single schema node. When a user
		// picked Serverless, managed-domain discovery returns empty —
		// falling back to AOSS keeps either deployment style resolving
		// to a non-empty discovery and prevents the drift false-positive
		// the InsideOut backend hit in #1036.
		return discoverOpenSearchUnion(ctx, opensearch.NewFromConfig(cfg), opensearchserverless.NewFromConfig(cfg), project)
	case "list-collections":
		return discoverOpenSearchServerless(ctx, opensearchserverless.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("opensearch")
	default:
		return nil, unsupportedActionError("opensearch", action)
	}
}

// discoverOpenSearchUnion merges managed-domain and AOSS results.
// Per-side errors are logged and skipped so an AOSS-only deploy in a
// region where managed ListDomainNames fails (transient API hiccup)
// still returns the serverless half. If both sides error, the managed
// error is surfaced as the primary signal — mirrors the InsideOut backend's pre-#1036
// behaviour for backward compatibility.
//
// Mirrors the InsideOut backend's discoverOpenSearchUnion (aws_inspect.go:1076).
func discoverOpenSearchUnion(ctx context.Context, managedClient opensearchAPI, serverlessClient aossAPI, project string) (any, error) {
	merged := []map[string]any{}

	managedRaw, mErr := discoverOpenSearchManaged(ctx, managedClient, project)
	if mErr != nil {
		log.Printf("[aws-inspect] opensearch managed discovery: %v (continuing to AOSS)", mErr)
	} else {
		merged = append(merged, opensearchResultToMaps(managedRaw)...)
	}

	serverlessRaw, sErr := discoverOpenSearchServerless(ctx, serverlessClient, project)
	if sErr != nil {
		log.Printf("[aws-inspect] opensearch serverless discovery: %v (continuing)", sErr)
	} else {
		merged = append(merged, opensearchResultToMaps(serverlessRaw)...)
	}

	if mErr != nil && sErr != nil {
		return merged, mErr
	}
	return merged, nil
}

// opensearchResultToMaps normalises the two return shapes (typed
// DomainStatus vs. []map[string]any) into a single slice so the union
// can concatenate them.
func opensearchResultToMaps(v any) []map[string]any {
	if v == nil {
		return nil
	}
	if maps, ok := v.([]map[string]any); ok {
		return maps
	}
	return toSliceOfMaps(v)
}

// discoverOpenSearchManaged returns matching managed OpenSearch domains.
// Filters by Project tag via opensearch.ListTags(arn); returns all when
// project filter is empty.
//
// Mirrors the InsideOut backend's discoverOpenSearchManaged (aws_inspect.go:1124).
func discoverOpenSearchManaged(ctx context.Context, client opensearchAPI, project string) (any, error) {
	listOut, err := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if err != nil {
		return []any{}, err
	}
	if len(listOut.DomainNames) == 0 {
		return []any{}, nil
	}
	var domainNames []string
	for _, d := range listOut.DomainNames {
		if d.DomainName != nil {
			domainNames = append(domainNames, *d.DomainName)
		}
	}
	descOut, err := client.DescribeDomains(ctx, &opensearch.DescribeDomainsInput{DomainNames: domainNames})
	if err != nil {
		return []any{}, err
	}
	if project == "" {
		return descOut.DomainStatusList, nil
	}
	matched := []map[string]any{}
	for _, d := range descOut.DomainStatusList {
		arn := aws.ToString(d.ARN)
		if arn == "" {
			continue
		}
		tagsOut, tagErr := client.ListTags(ctx, &opensearch.ListTagsInput{ARN: aws.String(arn)})
		if tagErr != nil {
			log.Printf("[aws-inspect] opensearch ListTags %s: %v (skipping)", arn, tagErr)
			continue
		}
		var tags []any
		for _, t := range tagsOut.TagList {
			tags = append(tags, map[string]any{"Key": aws.ToString(t.Key), "Value": aws.ToString(t.Value)})
		}
		if !filter.MatchesTag(tags, project, filter.FormatKV) {
			continue
		}
		matched = append(matched, toMapAny(d))
	}
	return matched, nil
}

// discoverOpenSearchServerless returns matching AOSS collections. Uses
// opensearchserverless.ListCollections + ListTagsForResource(arn) per
// collection.
//
// Mirrors the InsideOut backend's discoverOpenSearchServerless (aws_inspect.go:1173).
func discoverOpenSearchServerless(ctx context.Context, client aossAPI, project string) (any, error) {
	listOut, err := client.ListCollections(ctx, &opensearchserverless.ListCollectionsInput{})
	if err != nil {
		return []any{}, err
	}
	if len(listOut.CollectionSummaries) == 0 {
		return []any{}, nil
	}
	if project == "" {
		return toSliceOfMaps(listOut.CollectionSummaries), nil
	}
	matched := []map[string]any{}
	for _, c := range listOut.CollectionSummaries {
		arn := aws.ToString(c.Arn)
		if arn == "" {
			continue
		}
		tagsOut, tagErr := client.ListTagsForResource(ctx, &opensearchserverless.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
		if tagErr != nil {
			log.Printf("[aws-inspect] aoss ListTagsForResource %s: %v (skipping)", arn, tagErr)
			continue
		}
		var tags []any
		for _, t := range tagsOut.Tags {
			tags = append(tags, map[string]any{"Key": aws.ToString(t.Key), "Value": aws.ToString(t.Value)})
		}
		if filter.MatchesTag(tags, project, filter.FormatKV) {
			matched = append(matched, toMapAny(c))
		}
	}
	return matched, nil
}

// --- MSK ---

func inspectMSK(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := kafka.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "list-clusters":
		out, err := client.ListClusters(ctx, &kafka.ListClustersInput{})
		if err != nil {
			return nil, err
		}
		// MSK ClusterInfo carries Tags as map[string]string inline — no
		// fan-out needed, filter client-side via the map shape.
		if project != "" {
			return filter.Match(toSliceOfMaps(out.ClusterInfoList), project, "Tags", filter.FormatMap), nil
		}
		return nilSliceToEmpty(out.ClusterInfoList), nil
	case "get-metrics":
		return metricsRouted("msk")
	default:
		return nil, unsupportedActionError("msk", action)
	}
}
