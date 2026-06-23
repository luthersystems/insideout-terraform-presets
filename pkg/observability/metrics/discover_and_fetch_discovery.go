// discover_and_fetch_discovery.go
//
// Per-service ID extraction for the DiscoverAndFetch path. Ported from
// reliable's internal/agentapi/aws_metrics_discovery.go. Every
// service-specific SDK call + project-tag filter pair lives upstream in
// pkg/observability/discovery/aws; this file contributes only the
// CloudWatch-dimension ID shape: each upstream discovery action returns a
// typed slice (or, for the few services that delegate to filter.Match, a
// []map[string]any), and the extractor walks the shape pulling out the
// field that CloudWatch keys on.
//
// Special cases consolidated here:
//
//   - ALB: the CloudWatch dimension value is the ARN suffix
//     ("app/<name>/<hash>"), not the full ARN.
//   - SQS: the dimension is the queue name (last URL segment), not the
//     full queue URL.
//   - EC2: only running instances participate in metrics. Stopped /
//     terminated instances either have no metrics or stale data we don't
//     want to chart.
//   - Bedrock: no project-scoped model enumeration is possible
//     (foundation models are account-wide); the extractor returns nil to
//     keep the panel empty without erroring.
//   - VPC: AWS/NATGateway is the metric namespace, so we extract NAT
//     gateway IDs from the describe-nat-gateways result, not VPC IDs.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	apigatewayv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	cwlogstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"

	awsdiscovery "github.com/luthersystems/insideout-terraform-presets/pkg/observability/discovery/aws"
)

// metricsDiscoverySpec pairs a service with the upstream discovery
// action whose result feeds metric ID extraction. Each spec's Extract
// hands back the CloudWatch dimension values for that service.
type metricsDiscoverySpec struct {
	// Action is the upstream awsdiscovery.Inspect action name (e.g.
	// "describe-instances", "list-functions"). Selected to be the
	// cheapest call that returns the field CloudWatch keys on.
	Action string

	// Extract walks the upstream result and returns the CloudWatch
	// dimension values. Returns nil for an empty result. Must accept
	// both the typed-slice shape (project=="" branch) and the
	// []map[string]any shape (project!="" branch routed through
	// filter.Match) where applicable — RDS, MSK, APIGateway, OpenSearch.
	Extract func(any) []string
}

// metricsDiscoveryActions maps service tag → discovery spec. Keep in
// sync with metricDefinitions (the metric-spec catalog) — every key
// here must have an entry there, and vice versa, except for KMS and
// SecretsManager which take the operational-health path instead of
// CloudWatch.
var metricsDiscoveryActions = map[string]metricsDiscoverySpec{
	"ec2": {
		Action:  "describe-instances",
		Extract: extractEC2RunningInstanceIDs,
	},
	"lambda": {
		Action:  "list-functions",
		Extract: extractLambdaFunctionNames,
	},
	"alb": {
		Action:  "describe-load-balancers",
		Extract: extractALBDimensionIDs,
	},
	"rds": {
		Action:  "describe-db-instances",
		Extract: extractRDSInstanceIdentifiers,
	},
	"cloudfront": {
		Action:  "list-distributions",
		Extract: extractCloudFrontDistributionIDs,
	},
	"apigateway": {
		Action:  "get-apis",
		Extract: extractAPIGatewayIDs,
	},
	"vpc": {
		Action:  "describe-nat-gateways",
		Extract: extractNATGatewayIDs,
	},
	"s3": {
		Action:  "list-buckets",
		Extract: extractS3BucketNames,
	},
	"sqs": {
		Action:  "list-queues",
		Extract: extractSQSQueueNames,
	},
	"dynamodb": {
		Action:  "list-tables",
		Extract: extractStringSlice,
	},
	"cloudwatchlogs": {
		Action:  "describe-log-groups",
		Extract: extractCloudWatchLogGroupNames,
	},
	"cognito": {
		Action:  "list-user-pools",
		Extract: extractCognitoUserPoolIDs,
	},
	"opensearch": {
		Action:  "describe-domains",
		Extract: extractOpenSearchDomainNames,
	},
	"bedrock": {
		Action:  "list-knowledge-bases",
		Extract: extractBedrockNoMetrics,
	},
	"eks": {
		Action:  "list-clusters",
		Extract: extractStringSlice,
	},
	"elasticache": {
		Action:  "describe-cache-clusters",
		Extract: extractElastiCacheClusterIDs,
	},
	"msk": {
		Action:  "list-clusters",
		Extract: extractMSKClusterARNs,
	},
	"waf": {
		Action:  "list-web-acls",
		Extract: extractWAFWebACLIDs,
	},
	"ecs": {
		Action:  "list-clusters",
		Extract: extractECSClusterNames,
	},
}

// runMetricsDiscovery dispatches to the upstream Inspect for the given
// service and returns the CloudWatch dimension values. Project filter
// is encoded as JSON `{"project":"<p>"}` for upstream's filter.Project
// reader. Empty project means session-wide (demo); upstream skips the
// project tag check on empty.
//
// Declared as a seam var so tests can assert the resource-scoped fast
// path (reliable#2035) bypasses account-wide discovery without a real AWS
// round trip. Ported from reliable's runMetricsDiscovery.
var runMetricsDiscovery = runMetricsDiscoveryImpl

func runMetricsDiscoveryImpl(ctx context.Context, cfg aws.Config, service, project string) ([]string, error) {
	spec, ok := metricsDiscoveryActions[service]
	if !ok {
		return nil, fmt.Errorf("no auto-discovery for service: %s", service)
	}
	filters := ""
	if project != "" {
		if b, err := json.Marshal(map[string]string{"project": project}); err == nil {
			filters = string(b)
		}
	}
	raw, err := awsdiscovery.Inspect(ctx, cfg, service, spec.Action, filters)
	if err != nil {
		return nil, err
	}
	return spec.Extract(raw), nil
}

// --- Per-service extractors ---
//
// Each extractor takes the upstream Inspect result and returns the
// CloudWatch dimension values. Where upstream may return either a typed
// slice or a []map[string]any (project-filtered via filter.Match), the
// extractor handles both with type assertions.

func extractEC2RunningInstanceIDs(raw any) []string {
	reservations, ok := raw.([]ec2types.Reservation)
	if !ok {
		return nil
	}
	var ids []string
	for _, r := range reservations {
		for _, inst := range r.Instances {
			if inst.State == nil || inst.State.Name != ec2types.InstanceStateNameRunning {
				continue
			}
			if id := aws.ToString(inst.InstanceId); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func extractLambdaFunctionNames(raw any) []string {
	fns, ok := raw.([]lambdatypes.FunctionConfiguration)
	if !ok {
		return nil
	}
	var names []string
	for _, f := range fns {
		if name := aws.ToString(f.FunctionName); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func extractALBDimensionIDs(raw any) []string {
	lbs, ok := raw.([]elbv2types.LoadBalancer)
	if !ok {
		return nil
	}
	var ids []string
	for _, lb := range lbs {
		arn := aws.ToString(lb.LoadBalancerArn)
		if id := albDimensionFromARN(arn); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// albDimensionFromARN trims an ALB ARN to its CloudWatch dimension
// suffix. Example:
//
//	arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/my-lb/abc
//	→ app/my-lb/abc
func albDimensionFromARN(arn string) string {
	if _, suffix, ok := strings.Cut(arn, "loadbalancer/"); ok {
		return suffix
	}
	return ""
}

func extractRDSInstanceIdentifiers(raw any) []string {
	if dbs, ok := raw.([]rdstypes.DBInstance); ok {
		ids := make([]string, 0, len(dbs))
		for _, db := range dbs {
			if id := aws.ToString(db.DBInstanceIdentifier); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	// project!="" path returns []map[string]any after filter.Match.
	return extractStringField(raw, "DBInstanceIdentifier")
}

func extractCloudFrontDistributionIDs(raw any) []string {
	if dists, ok := raw.([]cloudfronttypes.DistributionSummary); ok {
		ids := make([]string, 0, len(dists))
		for _, d := range dists {
			if id := aws.ToString(d.Id); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return extractStringField(raw, "Id")
}

func extractAPIGatewayIDs(raw any) []string {
	if apis, ok := raw.([]apigatewayv2types.Api); ok {
		ids := make([]string, 0, len(apis))
		for _, a := range apis {
			if id := aws.ToString(a.ApiId); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return extractStringField(raw, "ApiId")
}

func extractNATGatewayIDs(raw any) []string {
	gws, ok := raw.([]ec2types.NatGateway)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(gws))
	for _, gw := range gws {
		if id := aws.ToString(gw.NatGatewayId); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func extractS3BucketNames(raw any) []string {
	buckets, ok := raw.([]s3types.Bucket)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(buckets))
	for _, b := range buckets {
		if name := aws.ToString(b.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func extractSQSQueueNames(raw any) []string {
	urls, ok := raw.([]string)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(urls))
	for _, u := range urls {
		if name := sqsQueueNameFromURL(u); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// sqsQueueNameFromURL extracts the queue name from a queue URL.
// Example: https://sqs.us-east-1.amazonaws.com/123/my-queue → my-queue.
func sqsQueueNameFromURL(queueURL string) string {
	idx := strings.LastIndex(queueURL, "/")
	if idx < 0 || idx == len(queueURL)-1 {
		return ""
	}
	return queueURL[idx+1:]
}

func extractStringSlice(raw any) []string {
	if names, ok := raw.([]string); ok {
		return names
	}
	return nil
}

func extractCloudWatchLogGroupNames(raw any) []string {
	groups, ok := raw.([]cwlogstypes.LogGroup)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		if name := aws.ToString(g.LogGroupName); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func extractCognitoUserPoolIDs(raw any) []string {
	pools, ok := raw.([]cognitoidptypes.UserPoolDescriptionType)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(pools))
	for _, p := range pools {
		if id := aws.ToString(p.Id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// extractOpenSearchDomainNames handles upstream's union return —
// describe-domains may yield typed []opensearchtypes.DomainStatus or
// []map[string]any depending on the project filter path. Both shapes
// carry "DomainName" / "domain_name" / similar keys.
func extractOpenSearchDomainNames(raw any) []string {
	// Try the maps path first since upstream's project-filtered branch
	// JSON-roundtrips through []map[string]any.
	if names := extractStringField(raw, "DomainName"); len(names) > 0 {
		return names
	}
	// Typed shape: []opensearchtypes.DomainStatus has DomainName too,
	// but reflecting on it would pull in another dep — fall through to
	// extractStringField after one more pass via JSON marshal/unmarshal.
	return extractStringFieldViaJSON(raw, "DomainName")
}

// extractBedrockNoMetrics returns nil regardless of the upstream
// payload — Bedrock CloudWatch metrics are per-foundation-model and
// foundation models aren't project-scoped. Keeping the empty panel
// matches the pre-Phase-H discoverer behaviour. See reliable#1017 / #1018.
func extractBedrockNoMetrics(_ any) []string {
	return nil
}

func extractElastiCacheClusterIDs(raw any) []string {
	clusters, ok := raw.([]elasticachetypes.CacheCluster)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(clusters))
	for _, c := range clusters {
		if id := aws.ToString(c.CacheClusterId); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func extractMSKClusterARNs(raw any) []string {
	if clusters, ok := raw.([]kafkatypes.Cluster); ok {
		arns := make([]string, 0, len(clusters))
		for _, c := range clusters {
			if arn := aws.ToString(c.ClusterArn); arn != "" {
				arns = append(arns, arn)
			}
		}
		return arns
	}
	return extractStringField(raw, "ClusterArn")
}

func extractWAFWebACLIDs(raw any) []string {
	acls, ok := raw.([]wafv2types.WebACLSummary)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(acls))
	for _, a := range acls {
		if id := aws.ToString(a.Id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func extractECSClusterNames(raw any) []string {
	clusters, ok := raw.([]ecstypes.Cluster)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(clusters))
	for _, c := range clusters {
		// AWS/ECS metrics are keyed on ClusterName (short), not ARN.
		// Walk the ARN suffix when ClusterName is absent on the SDK
		// response.
		if name := aws.ToString(c.ClusterName); name != "" {
			names = append(names, name)
			continue
		}
		arn := aws.ToString(c.ClusterArn)
		if idx := strings.LastIndex(arn, "/"); idx >= 0 && idx < len(arn)-1 {
			names = append(names, arn[idx+1:])
		}
	}
	return names
}

// extractStringField pulls a single string field out of a
// []map[string]any payload (upstream's filter.Match return shape). Used
// for the project-filtered branches on RDS / MSK / APIGateway. Returns
// nil if raw isn't []map[string]any or the field is absent / non-string
// on every entry.
func extractStringField(raw any, field string) []string {
	maps, ok := raw.([]map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(maps))
	for _, m := range maps {
		v, present := m[field]
		if !present {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// extractStringFieldViaJSON is a fallback for typed shapes the
// extractor doesn't statically know about. Marshals raw → JSON →
// []map[string]any then runs extractStringField. Only used for
// OpenSearch's typed describe-domains return shape, which carries
// DomainName under the same key after JSON encoding.
func extractStringFieldViaJSON(raw any, field string) []string {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var maps []map[string]any
	if err := json.Unmarshal(b, &maps); err != nil {
		return nil
	}
	out := make([]string, 0, len(maps))
	for _, m := range maps {
		if v, ok := m[field]; ok {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
