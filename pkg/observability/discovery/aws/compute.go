// Compute-family AWS service inspectors: EC2, EBS, Lambda, ECS, EKS.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go (ec2:328,
// ebs:380, ecs:635, eks:762, lambda:983) and the corresponding tag-filter
// helpers in aws_metrics.go (filterEKSClustersByProjectTag:1668,
// filterLambdaFunctionsByProjectTag:899). The per-service interface
// shapes (ecsClient, eksClustersClient, lambdaFunctionsClient) are kept
// narrow so test fakes only implement what the inspector calls — same
// pattern the InsideOut backend uses.

package aws

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// --- EC2 ---

func inspectEC2(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := ec2.NewFromConfig(cfg)
	tagFilters := filter.ProjectTagFilter(filter.Project(filters))

	switch action {
	case "describe-instances":
		input := &ec2.DescribeInstancesInput{}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeInstances(ctx, input)
		if err != nil {
			return nil, err
		}
		return enrichEC2WithConnectURLs(cfg.Region, out.Reservations), nil
	case "describe-vpcs":
		input := &ec2.DescribeVpcsInput{}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeVpcs(ctx, input)
		if err != nil {
			return nil, err
		}
		return out.Vpcs, nil
	case "describe-subnets":
		input := &ec2.DescribeSubnetsInput{}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeSubnets(ctx, input)
		if err != nil {
			return nil, err
		}
		return out.Subnets, nil
	case "describe-security-groups":
		input := &ec2.DescribeSecurityGroupsInput{}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeSecurityGroups(ctx, input)
		if err != nil {
			return nil, err
		}
		return out.SecurityGroups, nil
	case "get-metrics":
		return metricsRouted("ec2")
	default:
		return nil, unsupportedActionError("ec2", action)
	}
}

// --- EBS ---

func inspectEBS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := ec2.NewFromConfig(cfg)
	tagFilters := filter.ProjectTagFilter(filter.Project(filters))

	switch action {
	case "describe-volumes":
		input := &ec2.DescribeVolumesInput{}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeVolumes(ctx, input)
		if err != nil {
			return nil, err
		}
		return out.Volumes, nil
	case "describe-snapshots":
		// "self" excludes the millions of public/AWS-owned snapshots —
		// without this the call times out before pagination starts.
		input := &ec2.DescribeSnapshotsInput{OwnerIds: []string{"self"}}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeSnapshots(ctx, input)
		if err != nil {
			return nil, err
		}
		return out.Snapshots, nil
	default:
		return nil, unsupportedActionError("ebs", action)
	}
}

// --- Lambda ---

// lambdaFunctionsClient is the subset of the lambda SDK used by the
// shared Lambda filter helper. Mirrors the InsideOut backend's lambdaFunctionsClient
// (aws_metrics.go:875).
type lambdaFunctionsClient interface {
	ListFunctions(ctx context.Context, params *lambda.ListFunctionsInput, optFns ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
	ListTags(ctx context.Context, params *lambda.ListTagsInput, optFns ...func(*lambda.Options)) (*lambda.ListTagsOutput, error)
}

func inspectLambda(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-functions":
		return filterLambdaFunctionsByProjectTag(ctx, lambda.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("lambda")
	default:
		return nil, unsupportedActionError("lambda", action)
	}
}

// filterLambdaFunctionsByProjectTag paginates ListFunctions and, when
// project!="", fans out ListTags(FunctionArn) keeping only functions
// tagged Project=<project>. Per-function ListTags errors log+skip
// (fail-closed); ListFunctions errors abort so callers don't see a
// silently-truncated account scan.
//
// Mirrors the InsideOut backend's filterLambdaFunctionsByProjectTag (aws_metrics.go:899).
func filterLambdaFunctionsByProjectTag(ctx context.Context, client lambdaFunctionsClient, project string) ([]lambdatypes.FunctionConfiguration, error) {
	all := []lambdatypes.FunctionConfiguration{}
	paginator := lambda.NewListFunctionsPaginator(client, &lambda.ListFunctionsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("lambda ListFunctions: %w", err)
		}
		all = append(all, page.Functions...)
	}
	if project == "" {
		return all, nil
	}
	matched := make([]lambdatypes.FunctionConfiguration, 0, len(all))
	for _, fn := range all {
		arn := aws.ToString(fn.FunctionArn)
		tagsOut, err := client.ListTags(ctx, &lambda.ListTagsInput{Resource: aws.String(arn)})
		if err != nil {
			log.Printf("[lambda ListTags] skip arn=%s: %v", arn, err)
			continue
		}
		if tagsOut.Tags["Project"] == project {
			matched = append(matched, fn)
		}
	}
	return matched, nil
}

// --- ECS ---

// ecsClient is the subset of the ECS SDK used by the cluster/service
// filter helpers below. Narrowed so test fakes implement only the four
// ops we call. Mirrors the InsideOut backend's ecsClient (aws_inspect.go:628).
type ecsClient interface {
	ListClusters(ctx context.Context, in *ecs.ListClustersInput, opts ...func(*ecs.Options)) (*ecs.ListClustersOutput, error)
	DescribeClusters(ctx context.Context, in *ecs.DescribeClustersInput, opts ...func(*ecs.Options)) (*ecs.DescribeClustersOutput, error)
	ListServices(ctx context.Context, in *ecs.ListServicesInput, opts ...func(*ecs.Options)) (*ecs.ListServicesOutput, error)
	DescribeServices(ctx context.Context, in *ecs.DescribeServicesInput, opts ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error)
}

func inspectECS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-clusters":
		return filterECSClustersByProjectTag(ctx, ecs.NewFromConfig(cfg), project)
	case "list-services":
		return listECSServicesAcrossClusters(ctx, ecs.NewFromConfig(cfg), project)
	case "describe-services":
		return describeECSServicesAcrossClusters(ctx, ecs.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("ecs")
	default:
		return nil, unsupportedActionError("ecs", action)
	}
}

// filterECSClustersByProjectTag lists every cluster ARN, fans out
// DescribeClusters (with Tags include) in 100-ARN batches (the API
// limit), and returns clusters whose Project tag matches. Empty project
// returns every cluster unchanged.
//
// Mirrors the InsideOut backend's filterECSClustersByProjectTag (aws_inspect.go:657).
func filterECSClustersByProjectTag(ctx context.Context, client ecsClient, project string) ([]ecstypes.Cluster, error) {
	var arns []string
	paginator := ecs.NewListClustersPaginator(client, &ecs.ListClustersInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("ecs ListClusters: %w", err)
		}
		arns = append(arns, page.ClusterArns...)
	}
	if len(arns) == 0 {
		return []ecstypes.Cluster{}, nil
	}
	all := []ecstypes.Cluster{}
	for i := 0; i < len(arns); i += 100 {
		end := i + 100
		if end > len(arns) {
			end = len(arns)
		}
		out, err := client.DescribeClusters(ctx, &ecs.DescribeClustersInput{
			Clusters: arns[i:end],
			Include:  []ecstypes.ClusterField{ecstypes.ClusterFieldTags},
		})
		if err != nil {
			return nil, fmt.Errorf("ecs DescribeClusters: %w", err)
		}
		all = append(all, out.Clusters...)
	}
	if project == "" {
		return all, nil
	}
	matched := make([]ecstypes.Cluster, 0, len(all))
	for _, c := range all {
		if hasProjectTagECS(c.Tags, project) {
			matched = append(matched, c)
		}
	}
	return matched, nil
}

// listECSServicesAcrossClusters returns cluster-ARN → []service-ARN for
// every project-tagged cluster.
//
// Mirrors the InsideOut backend's listECSServicesAcrossClusters (aws_inspect.go:698).
func listECSServicesAcrossClusters(ctx context.Context, client ecsClient, project string) (map[string][]string, error) {
	clusters, err := filterECSClustersByProjectTag(ctx, client, project)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(clusters))
	for _, c := range clusters {
		arn := aws.ToString(c.ClusterArn)
		if arn == "" {
			continue
		}
		var svcArns []string
		p := ecs.NewListServicesPaginator(client, &ecs.ListServicesInput{Cluster: &arn})
		for p.HasMorePages() {
			page, perr := p.NextPage(ctx)
			if perr != nil {
				return nil, fmt.Errorf("ecs ListServices cluster=%s: %w", arn, perr)
			}
			svcArns = append(svcArns, page.ServiceArns...)
		}
		out[arn] = svcArns
	}
	return out, nil
}

// describeECSServicesAcrossClusters batches DescribeServices 10 at a
// time (ECS API limit) across every project-tagged cluster.
//
// Mirrors the InsideOut backend's describeECSServicesAcrossClusters (aws_inspect.go:726).
func describeECSServicesAcrossClusters(ctx context.Context, client ecsClient, project string) ([]ecstypes.Service, error) {
	serviceArnsByCluster, err := listECSServicesAcrossClusters(ctx, client, project)
	if err != nil {
		return nil, err
	}
	all := []ecstypes.Service{}
	for cluster, arns := range serviceArnsByCluster {
		for i := 0; i < len(arns); i += 10 {
			end := i + 10
			if end > len(arns) {
				end = len(arns)
			}
			out, derr := client.DescribeServices(ctx, &ecs.DescribeServicesInput{
				Cluster:  &cluster,
				Services: arns[i:end],
			})
			if derr != nil {
				return nil, fmt.Errorf("ecs DescribeServices cluster=%s: %w", cluster, derr)
			}
			all = append(all, out.Services...)
		}
	}
	return all, nil
}

// hasProjectTagECS matches Project=<project> against ECS's
// [{Key,Value}] tag list. Case-sensitive — mirrors AWS tag semantics.
func hasProjectTagECS(tags []ecstypes.Tag, project string) bool {
	if project == "" {
		return true
	}
	for _, t := range tags {
		if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
			return true
		}
	}
	return false
}

// --- EKS ---

// eksClustersClient is the subset of the eks SDK used by the cluster
// filter helper. Mirrors the InsideOut backend's eksClustersClient (aws_metrics.go:1653).
type eksClustersClient interface {
	ListClusters(ctx context.Context, params *eks.ListClustersInput, optFns ...func(*eks.Options)) (*eks.ListClustersOutput, error)
	DescribeCluster(ctx context.Context, params *eks.DescribeClusterInput, optFns ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
}

func inspectEKS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-clusters":
		return filterEKSClustersByProjectTag(ctx, eks.NewFromConfig(cfg), project)
	case "describe-cluster":
		// the InsideOut backend returned an explicit "not yet implemented fully" here
		// because the action requires a cluster name in filters that the
		// session-based dispatcher couldn't surface. We preserve that
		// stub so the action is recognized (drift gate passes) but
		// callers that need a single cluster's details know to call
		// DescribeCluster directly with the cluster name.
		return nil, fmt.Errorf("describe-cluster requires a cluster name in filters (not yet implemented)")
	case "list-nodes":
		return listEKSNodeInstances(ctx, eks.NewFromConfig(cfg), ec2.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("eks")
	default:
		// Every sibling inspector returns unsupportedActionError on an
		// unknown action — matching that here keeps the contract
		// uniform across the dispatcher (a typo'd action like
		// "list-cluster" should fail loudly, not silently return the
		// cluster list). Diverges intentionally from the InsideOut backend's
		// inspectEKS, which used the cluster list as a default fallback.
		// #204 P2.
		return nil, unsupportedActionError("eks", action)
	}
}

// ec2InstancesClient is the subset of the ec2 SDK used by
// listEKSNodeInstances. Narrow per-handler interface so test fakes
// only implement what the helper calls — same pattern used by the
// other per-service inspector clients in this file.
type ec2InstancesClient interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// listEKSNodeInstances pivots EKS metric discovery from cluster-name
// to EC2 InstanceId via the AWS-managed `eks:cluster-name` tag, so the
// observability panel can query AWS/EC2 CPUUtilization per node instead
// of the unpopulated AWS/EKS namespace (#231 / Option A — works on
// existing deployments without the amazon-cloudwatch-observability
// addon).
//
// Lists clusters by Project tag, then for each cluster lists EC2
// instances tagged Project=<project> AND eks:cluster-name=<cluster>
// (an AWS-managed tag the EKS managed node group attaches to its
// underlying ASG / EC2 instances), returning the deduped flat list of
// instance IDs. Per-cluster errors log+skip — partial result beats
// empty when one cluster has an IAM denial or throttle, matching the
// contract every other tag-fan-out helper in this package follows.
//
// The ContainerInsights namespace would surface richer node + pod
// metrics but requires the amazon-cloudwatch-observability addon,
// which our aws/eks_nodegroup preset does not install today (#231
// follow-up Option B).
func listEKSNodeInstances(ctx context.Context, eksClient eksClustersClient, ec2Client ec2InstancesClient, project string) ([]string, error) {
	clusters, err := filterEKSClustersByProjectTag(ctx, eksClient, project)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	instances := []string{}
	for _, cluster := range clusters {
		filters := []ec2types.Filter{{
			Name:   aws.String("tag:eks:cluster-name"),
			Values: []string{cluster},
		}}
		if project != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("tag:Project"),
				Values: []string{project},
			})
		}
		out, descErr := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: filters})
		if descErr != nil {
			log.Printf("[eks list-nodes] cluster=%s DescribeInstances skip: %v", cluster, descErr)
			continue
		}
		for _, res := range out.Reservations {
			for _, inst := range res.Instances {
				if inst.InstanceId == nil {
					continue
				}
				id := aws.ToString(inst.InstanceId)
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				instances = append(instances, id)
			}
		}
	}
	return instances, nil
}

// filterEKSClustersByProjectTag lists clusters and, when project!="",
// fans out DescribeCluster per name to check inline Tags. Per-cluster
// DescribeCluster errors log+skip so one bad cluster does not wipe the
// whole pass.
//
// Mirrors the InsideOut backend's filterEKSClustersByProjectTag (aws_metrics.go:1668).
func filterEKSClustersByProjectTag(ctx context.Context, client eksClustersClient, project string) ([]string, error) {
	names := []string{}
	paginator := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("eks ListClusters: %w", err)
		}
		names = append(names, page.Clusters...)
	}
	if project == "" {
		return names, nil
	}
	matched := []string{}
	for _, name := range names {
		descOut, descErr := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
		if descErr != nil {
			log.Printf("[eks DescribeCluster] skip cluster=%s: %v", name, descErr)
			continue
		}
		if descOut.Cluster != nil && descOut.Cluster.Tags["Project"] == project {
			matched = append(matched, name)
		}
	}
	return matched, nil
}
