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
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// --- EC2 ---

func inspectEC2(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := ec2.NewFromConfig(cfg)
	tagFilters := ProjectTagFilter(filter.Project(filters))

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
		return nilSliceToEmpty(out.Vpcs), nil
	case "describe-subnets":
		input := &ec2.DescribeSubnetsInput{}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeSubnets(ctx, input)
		if err != nil {
			return nil, err
		}
		return nilSliceToEmpty(out.Subnets), nil
	case "describe-security-groups":
		input := &ec2.DescribeSecurityGroupsInput{}
		if len(tagFilters) > 0 {
			input.Filters = tagFilters
		}
		out, err := client.DescribeSecurityGroups(ctx, input)
		if err != nil {
			return nil, err
		}
		return nilSliceToEmpty(out.SecurityGroups), nil
	case "get-metrics":
		return metricsRouted("ec2")
	default:
		return nil, unsupportedActionError("ec2", action)
	}
}

// --- EBS ---

func inspectEBS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := ec2.NewFromConfig(cfg)
	tagFilters := ProjectTagFilter(filter.Project(filters))

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
		return nilSliceToEmpty(out.Volumes), nil
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
		return nilSliceToEmpty(out.Snapshots), nil
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
	// Building the client makes no network call, so the unknown-action
	// and get-metrics paths can share it.
	return inspectECSWithClient(ctx, ecs.NewFromConfig(cfg), action, filter.Project(filters))
}

// inspectECSWithClient is the action switch of inspectECS, split out so
// tests can pass a fake client and count the calls each action makes.
func inspectECSWithClient(ctx context.Context, client ecsClient, action, project string) (any, error) {
	switch action {
	case "list-clusters":
		// list-clusters is the ECS panel default and also feeds the
		// metrics discovery (extractECSClusterNames), so it stays one
		// ListClusters + DescribeClusters pass. The SETTINGS include
		// rides on that same DescribeClusters call.
		return filterECSClustersByProjectTag(ctx, client, project)
	case "describe-clusters":
		// The Service Connect answer costs a ListServices +
		// DescribeServices pair per cluster, so it lives in its own
		// action that only the drift read asks for (#927), not on every
		// panel load.
		clusters, err := filterECSClustersByProjectTag(ctx, client, project)
		if err != nil {
			return nil, err
		}
		return enrichECSServiceConnect(ctx, client, clusters), nil
	case "list-services":
		return listECSServicesAcrossClusters(ctx, client, project)
	case "describe-services":
		return describeECSServicesAcrossClusters(ctx, client, project)
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
// The Include set also asks for SETTINGS, so each cluster carries its
// containerInsights setting for extractECSConfig (#927) at no extra API
// call. The cluster's CapacityProviders and DefaultCapacityProviderStrategy
// come back without any Include value (there is no CAPACITY_PROVIDERS
// ClusterField; sending an unknown value fails the whole call).
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
			Include:  []ecstypes.ClusterField{ecstypes.ClusterFieldTags, ecstypes.ClusterFieldSettings},
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

// ecsCluster is one row of the ecs describe-clusters result: the SDK cluster
// (embedded, so the JSON keeps the pre-#927 shape) plus
// ServiceConnectEnabled. Service Connect is a per-service setting, so no
// DescribeClusters field answers aws_ecs.enableServiceConnect; it takes
// a ListServices + DescribeServices pair per cluster.
//
// ServiceConnectEnabled is nil (and omitted from the JSON) when the
// answer is unknown: the cluster is past configFanOutLimit, a call failed,
// or the cluster has no services and no Service Connect default
// namespace yet. The aws/ecs preset creates the namespace but no
// services, so a fresh stack reports unknown rather than a false "off".
type ecsCluster struct {
	ecstypes.Cluster
	ServiceConnectEnabled *bool `json:"ServiceConnectEnabled,omitempty"`
}

// ecsServiceConnectSampleSize is how many services per cluster
// enrichECSServiceConnect describes: one DescribeServices call takes at
// most ten services (the ECS API limit), and one call per cluster is the
// bound. Services in one stack share the Service Connect choice, so ten
// is a fair sample.
const ecsServiceConnectSampleSize = 10

// enrichECSServiceConnect wraps each cluster in an ecsCluster and works
// out ServiceConnectEnabled for the first configFanOutLimit clusters:
// true when the cluster has a Service Connect default namespace or any
// sampled service has Service Connect on, false when it has services
// and none of the sampled ones has it on (in its PRIMARY deployment). A failed call is logged and
// leaves the field unknown; it never fails the list.
func enrichECSServiceConnect(ctx context.Context, client ecsClient, clusters []ecstypes.Cluster) []ecsCluster {
	out := make([]ecsCluster, 0, len(clusters))
	for i, c := range clusters {
		row := ecsCluster{Cluster: c}
		if i < configFanOutLimit {
			row.ServiceConnectEnabled = ecsClusterServiceConnect(ctx, client, c)
		}
		out = append(out, row)
	}
	return out
}

func ecsClusterServiceConnect(ctx context.Context, client ecsClient, c ecstypes.Cluster) *bool {
	if c.ServiceConnectDefaults != nil && aws.ToString(c.ServiceConnectDefaults.Namespace) != "" {
		return aws.Bool(true)
	}
	arn := aws.ToString(c.ClusterArn)
	if arn == "" {
		return nil
	}
	listOut, err := client.ListServices(ctx, &ecs.ListServicesInput{
		Cluster:    aws.String(arn),
		MaxResults: aws.Int32(ecsServiceConnectSampleSize),
	})
	if err != nil {
		log.Printf("[ecs ListServices] cluster=%s: %v (service connect unknown)", arn, err)
		return nil
	}
	if len(listOut.ServiceArns) == 0 {
		return nil
	}
	descOut, err := client.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  aws.String(arn),
		Services: listOut.ServiceArns,
	})
	if err != nil {
		log.Printf("[ecs DescribeServices] cluster=%s: %v (service connect unknown)", arn, err)
		return nil
	}
	if len(descOut.Services) == 0 {
		return nil
	}
	for _, svc := range descOut.Services {
		if ecsServiceHasServiceConnect(svc) {
			return aws.Bool(true)
		}
	}
	return aws.Bool(false)
}

// ecsServiceHasServiceConnect reports whether the service's PRIMARY
// deployment (the one serving traffic) has Service Connect on. The ECS
// Service type carries the setting only on its deployments, not at the
// top level.
func ecsServiceHasServiceConnect(svc ecstypes.Service) bool {
	for _, d := range svc.Deployments {
		if aws.ToString(d.Status) != "PRIMARY" {
			continue
		}
		if d.ServiceConnectConfiguration != nil && d.ServiceConnectConfiguration.Enabled {
			return true
		}
	}
	return false
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
	ListNodegroups(ctx context.Context, params *eks.ListNodegroupsInput, optFns ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error)
	DescribeNodegroup(ctx context.Context, params *eks.DescribeNodegroupInput, optFns ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error)
}

func inspectEKS(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-clusters":
		return filterEKSClustersByProjectTag(ctx, eks.NewFromConfig(cfg), project)
	case "describe-clusters":
		// The list-clusters result is a bare array of names that callers
		// render as is, so the config read for drift (#927) is a separate
		// action that returns one object per cluster.
		client := eks.NewFromConfig(cfg)
		matches, err := filterEKSClustersByProjectTagDetailed(ctx, client, project)
		if err != nil {
			return nil, err
		}
		return describeEKSClusters(ctx, client, matches), nil
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
	matches, err := filterEKSClustersByProjectTagDetailed(ctx, client, project)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m.name)
	}
	return names, nil
}

// eksClusterMatch is a project cluster found by
// filterEKSClustersByProjectTagDetailed. cluster is the DescribeCluster
// answer the tag check already fetched, kept so describeEKSClusters does
// not describe the cluster a second time; it is nil on the no-project
// path, which skips the tag check.
type eksClusterMatch struct {
	name    string
	cluster *ekstypes.Cluster
}

// filterEKSClustersByProjectTagDetailed is filterEKSClustersByProjectTag
// that also returns each matched cluster's DescribeCluster answer.
func filterEKSClustersByProjectTagDetailed(ctx context.Context, client eksClustersClient, project string) ([]eksClusterMatch, error) {
	names := []string{}
	paginator := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("eks ListClusters: %w", err)
		}
		names = append(names, page.Clusters...)
	}
	matched := make([]eksClusterMatch, 0, len(names))
	if project == "" {
		for _, name := range names {
			matched = append(matched, eksClusterMatch{name: name})
		}
		return matched, nil
	}
	for _, name := range names {
		descOut, descErr := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
		if descErr != nil {
			log.Printf("[eks DescribeCluster] skip cluster=%s: %v", name, descErr)
			continue
		}
		if descOut.Cluster != nil && descOut.Cluster.Tags["Project"] == project {
			matched = append(matched, eksClusterMatch{name: name, cluster: descOut.Cluster})
		}
	}
	return matched, nil
}

// eksCluster is one row of the eks describe-clusters result (#927).
// list-clusters returns only names, so the design-vs-deployed card could
// compare nothing but the cluster count. This row also carries the
// control-plane settings from DescribeCluster and the scaling settings
// of the cluster's managed node groups from DescribeNodegroup, the
// values behind the aws_eks design config (controlPlaneVisibility,
// desiredSize, minSize, maxSize, instanceType).
//
// The config fields are omitted from the JSON when they were not
// fetched: the cluster is past configFanOutLimit, or a call failed.
type eksCluster struct {
	Name                  string         `json:"Name"`
	Version               *string        `json:"Version,omitempty"`
	EndpointPublicAccess  *bool          `json:"EndpointPublicAccess,omitempty"`
	EndpointPrivateAccess *bool          `json:"EndpointPrivateAccess,omitempty"`
	NodeGroups            []eksNodeGroup `json:"NodeGroups,omitempty"`
}

// eksNodeGroup is the scaling config of one managed node group.
type eksNodeGroup struct {
	Name          string   `json:"Name"`
	InstanceTypes []string `json:"InstanceTypes,omitempty"`
	DesiredSize   *int32   `json:"DesiredSize,omitempty"`
	MinSize       *int32   `json:"MinSize,omitempty"`
	MaxSize       *int32   `json:"MaxSize,omitempty"`
}

// describeEKSClusters builds the describe-clusters rows and reads the
// config of the first configFanOutLimit clusters: DescribeCluster (reused
// from the tag check when it ran), then ListNodegroups and DescribeNodegroup
// for up to configFanOutLimit node groups per cluster. Every failed call
// is logged and leaves its fields out; none fails the list.
func describeEKSClusters(ctx context.Context, client eksClustersClient, matches []eksClusterMatch) []eksCluster {
	out := make([]eksCluster, 0, len(matches))
	for i, m := range matches {
		row := eksCluster{Name: m.name}
		if i < configFanOutLimit {
			readEKSClusterConfig(ctx, client, m, &row)
		}
		out = append(out, row)
	}
	return out
}

func readEKSClusterConfig(ctx context.Context, client eksClustersClient, m eksClusterMatch, row *eksCluster) {
	cluster := m.cluster
	if cluster == nil {
		descOut, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(m.name)})
		if err != nil {
			log.Printf("[eks DescribeCluster] cluster=%s: %v (control-plane config omitted)", m.name, err)
		} else {
			cluster = descOut.Cluster
		}
	}
	if cluster != nil {
		row.Version = cluster.Version
		if vpc := cluster.ResourcesVpcConfig; vpc != nil {
			row.EndpointPublicAccess = aws.Bool(vpc.EndpointPublicAccess)
			row.EndpointPrivateAccess = aws.Bool(vpc.EndpointPrivateAccess)
		}
	}

	listOut, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{
		ClusterName: aws.String(m.name),
		MaxResults:  aws.Int32(configFanOutLimit),
	})
	if err != nil {
		log.Printf("[eks ListNodegroups] cluster=%s: %v (node-group config omitted)", m.name, err)
		return
	}
	for _, ngName := range listOut.Nodegroups {
		ngOut, err := client.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(m.name),
			NodegroupName: aws.String(ngName),
		})
		if err != nil || ngOut.Nodegroup == nil {
			log.Printf("[eks DescribeNodegroup] cluster=%s nodegroup=%s: %v (node group omitted)", m.name, ngName, err)
			continue
		}
		ng := eksNodeGroup{Name: ngName, InstanceTypes: ngOut.Nodegroup.InstanceTypes}
		if sc := ngOut.Nodegroup.ScalingConfig; sc != nil {
			ng.DesiredSize = sc.DesiredSize
			ng.MinSize = sc.MinSize
			ng.MaxSize = sc.MaxSize
		}
		row.NodeGroups = append(row.NodeGroups, ng)
	}
}
