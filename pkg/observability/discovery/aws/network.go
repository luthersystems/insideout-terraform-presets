// Network-tier AWS service inspectors: VPC, ALB, WAF, CloudFront,
// APIGateway.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go (vpc:444,
// alb:814, waf:858, cloudfront:775, apigateway:996) plus the helpers in
// aws_metrics.go (filterCloudFrontDistributionsByProjectTag:1077,
// filterWAFWebACLsByScope:1867, filterELBv2ARNsByProjectTag:1934).
//
// Two cross-region quirks survive intact: CloudFront requires
// us-east-1 (it's a global service), and the WAF inspector queries both
// the regional client AND a us-east-1-pinned client because CLOUDFRONT-
// scoped WebACLs only exist in us-east-1 — failing to merge those would
// hide every CloudFront-fronted ACL from drift detection.

package aws

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// --- VPC ---

// vpcDescribeAPI is the subset of the EC2 SDK used by inspectVPCWithIGW.
// Mirrors the InsideOut backend's vpcDescribeAPI (aws_inspect.go:478).
type vpcDescribeAPI interface {
	DescribeVpcs(ctx context.Context, in *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeInternetGateways(ctx context.Context, in *ec2.DescribeInternetGatewaysInput, opts ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error)
}

// vpcWithIGW is inspectVPCWithIGW's response element: the AWS SDK Vpc
// type extended with an IGW-attachment bool computed via a
// DescribeInternetGateways join. AWS's API doesn't carry this on the Vpc
// resource itself (IGW attachments live on the IGW side), so this
// wrapper documents the derived relationship explicitly. The embed means
// JSON output flattens the SDK's VpcId/CidrBlock/State/Tags/... at the
// top level and appends HasInternetGateway alongside — the shape the
// extractor (extractVPCConfig) expects.
//
// Mirrors the InsideOut backend's vpcWithIGW (aws_inspect.go:492).
type vpcWithIGW struct {
	ec2types.Vpc
	HasInternetGateway bool `json:"HasInternetGateway"`
}

func inspectVPC(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	switch action {
	case "describe-nat-gateways":
		client := ec2.NewFromConfig(cfg)
		input := &ec2.DescribeNatGatewaysInput{}
		if tagFilters := filter.ProjectTagFilter(filter.Project(filters)); len(tagFilters) > 0 {
			// NAT gateway DescribeNatGatewaysInput uses Filter (singular),
			// not Filters — peculiarity of the EC2 SDK's NAT API.
			input.Filter = tagFilters
		}
		out, err := client.DescribeNatGateways(ctx, input)
		if err != nil {
			return nil, err
		}
		return out.NatGateways, nil
	case "describe-vpcs":
		// Enrich with IGW attachments so extractVPCConfig can report
		// deploymentType=public|private without a second inspector
		// round-trip. Public ⇔ has-IGW; NAT-only is private.
		return inspectVPCWithIGW(ctx, ec2.NewFromConfig(cfg), filter.Project(filters))
	case "get-metrics":
		return metricsRouted("vpc")
	default:
		// the InsideOut backend's default path falls back to inspectEC2 so the VPC
		// service key accepts any EC2 action (describe-subnets, etc.).
		// Replicate to keep the contract identical.
		return inspectEC2(ctx, cfg, action, filters)
	}
}

// inspectVPCWithIGW fetches VPCs and their Internet Gateway attachments
// and returns the VPCs as []vpcWithIGW. HasInternetGateway is derived
// from each IGW's Attachments[].VpcId matching the VPC's VpcId — the
// same definition AWS uses for the public/private VPC distinction the
// preset emits.
//
// Tag filtering applies to DescribeVpcs only. IGWs are global per-account,
// so DescribeInternetGateways is scoped with attachment.vpc-id filter
// built from the VPCs just returned to keep the response small on shared
// accounts. A DescribeInternetGateways failure is logged but non-fatal
// — we still return the VPC inventory; extractor falls back to
// HasInternetGateway=false (which extractVPCConfig surfaces as
// deploymentType=private).
//
// Mirrors the InsideOut backend's inspectVPCWithIGW (aws_inspect.go:507).
func inspectVPCWithIGW(ctx context.Context, client vpcDescribeAPI, project string) ([]vpcWithIGW, error) {
	vpcInput := &ec2.DescribeVpcsInput{}
	if tagFilters := filter.ProjectTagFilter(project); len(tagFilters) > 0 {
		vpcInput.Filters = tagFilters
	}
	vpcOut, err := client.DescribeVpcs(ctx, vpcInput)
	if err != nil {
		return nil, err
	}
	if len(vpcOut.Vpcs) == 0 {
		return []vpcWithIGW{}, nil
	}
	vpcIDs := make([]string, 0, len(vpcOut.Vpcs))
	for _, v := range vpcOut.Vpcs {
		if id := aws.ToString(v.VpcId); id != "" {
			vpcIDs = append(vpcIDs, id)
		}
	}
	igwInput := &ec2.DescribeInternetGatewaysInput{}
	if len(vpcIDs) > 0 {
		igwInput.Filters = []ec2types.Filter{
			{
				Name:   aws.String("attachment.vpc-id"),
				Values: vpcIDs,
			},
		}
	}
	igwOut, igwErr := client.DescribeInternetGateways(ctx, igwInput)
	attachedVPCs := make(map[string]bool)
	if igwErr == nil && igwOut != nil {
		for _, igw := range igwOut.InternetGateways {
			for _, att := range igw.Attachments {
				if att.VpcId != nil {
					attachedVPCs[aws.ToString(att.VpcId)] = true
				}
			}
		}
	}
	if igwErr != nil {
		log.Printf("inspectVPC: DescribeInternetGateways failed (non-fatal): %v", igwErr)
	}
	vpcs := make([]vpcWithIGW, 0, len(vpcOut.Vpcs))
	for _, v := range vpcOut.Vpcs {
		vpcs = append(vpcs, vpcWithIGW{
			Vpc:                v,
			HasInternetGateway: attachedVPCs[aws.ToString(v.VpcId)],
		})
	}
	return vpcs, nil
}

// --- ALB ---

// elbv2TagsAPI is the subset of the ELBv2 SDK used for tag-based ALB
// filtering. Mirrors the InsideOut backend's elbv2TagsAPI (aws_metrics.go:1918).
type elbv2TagsAPI interface {
	DescribeLoadBalancers(ctx context.Context, in *elasticloadbalancingv2.DescribeLoadBalancersInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error)
	DescribeTags(ctx context.Context, in *elasticloadbalancingv2.DescribeTagsInput, opts ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error)
}

func inspectALB(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := elasticloadbalancingv2.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "describe-load-balancers":
		out, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
		if err != nil {
			return nil, err
		}
		// ALB DescribeLoadBalancers omits tags inline — fan out to
		// DescribeTags. Tag-based matching avoids substring false
		// positives the prior name-prefix approach would hit (e.g.
		// project "foo" matching "foobar"); preset PR #82 +
		// lint-project-tag.sh guarantees Project tags across every
		// taggable ALB-module resource.
		if project != "" {
			arns := make([]string, 0, len(out.LoadBalancers))
			for _, lb := range out.LoadBalancers {
				if arn := aws.ToString(lb.LoadBalancerArn); arn != "" {
					arns = append(arns, arn)
				}
			}
			matchedArns, tagErr := filterELBv2ARNsByProjectTag(ctx, client, arns, project)
			if tagErr != nil {
				return nil, tagErr
			}
			filtered := []map[string]any{}
			for _, lb := range toSliceOfMaps(out.LoadBalancers) {
				if _, ok := matchedArns[getString(lb, "LoadBalancerArn")]; ok {
					filtered = append(filtered, lb)
				}
			}
			return filtered, nil
		}
		return out.LoadBalancers, nil
	case "get-metrics":
		return metricsRouted("alb")
	default:
		return nil, unsupportedActionError("alb", action)
	}
}

// filterELBv2ARNsByProjectTag returns the subset of input ARNs whose
// Project tag matches `project`. ELBv2 DescribeTags accepts at most 20
// ARNs per call so callers with more get batched automatically.
//
// Mirrors the InsideOut backend's filterELBv2ARNsByProjectTag (aws_metrics.go:1934).
func filterELBv2ARNsByProjectTag(ctx context.Context, client elbv2TagsAPI, arns []string, project string) (map[string]struct{}, error) {
	matched := make(map[string]struct{}, len(arns))
	if project == "" || len(arns) == 0 {
		return matched, nil
	}
	const batch = 20
	for i := 0; i < len(arns); i += batch {
		end := i + batch
		if end > len(arns) {
			end = len(arns)
		}
		out, err := client.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{
			ResourceArns: arns[i:end],
		})
		if err != nil {
			return nil, fmt.Errorf("elbv2 DescribeTags: %w", err)
		}
		for _, td := range out.TagDescriptions {
			for _, t := range td.Tags {
				if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
					matched[aws.ToString(td.ResourceArn)] = struct{}{}
					break
				}
			}
		}
	}
	return matched, nil
}

// --- WAF ---

// wafv2WebACLsClient is the subset of the wafv2 SDK used by the shared
// WAF filter helper. Mirrors the InsideOut backend's wafv2WebACLsClient
// (aws_metrics.go:1835).
type wafv2WebACLsClient interface {
	ListWebACLs(ctx context.Context, params *wafv2.ListWebACLsInput, optFns ...func(*wafv2.Options)) (*wafv2.ListWebACLsOutput, error)
	ListTagsForResource(ctx context.Context, params *wafv2.ListTagsForResourceInput, optFns ...func(*wafv2.Options)) (*wafv2.ListTagsForResourceOutput, error)
}

func inspectWAF(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)

	switch action {
	case "list-web-acls":
		// WAFv2 requires a Scope. REGIONAL ACLs protect ALB / API
		// Gateway resources and live in the project's region.
		// CLOUDFRONT ACLs protect CloudFront distributions and only
		// exist in us-east-1. Query both and merge so a CF-fronted ACL
		// isn't invisible.
		regional, err := filterWAFWebACLsByScope(ctx, wafv2.NewFromConfig(cfg), wafv2types.ScopeRegional, project)
		if err != nil {
			return nil, err
		}
		cfCfg := cfg
		cfCfg.Region = "us-east-1"
		cloudfrontScoped, cfErr := filterWAFWebACLsByScope(ctx, wafv2.NewFromConfig(cfCfg), wafv2types.ScopeCloudfront, project)
		if cfErr != nil {
			// CLOUDFRONT failure (e.g. insufficient us-east-1 IAM)
			// degrades gracefully — drop CF-scoped, keep regional.
			log.Printf("[wafv2] CloudFront-scoped WAF query failed (continuing with regional only): %v", cfErr)
			cloudfrontScoped = nil
		}
		return append(regional, cloudfrontScoped...), nil
	case "get-metrics":
		return metricsRouted("waf")
	default:
		return nil, unsupportedActionError("waf", action)
	}
}

// filterWAFWebACLsByScope enumerates WebACLs for the given scope and
// applies the Project-tag filter. Shared between Regional (caller
// region) and CLOUDFRONT (us-east-1) scopes.
//
// Mirrors the InsideOut backend's filterWAFWebACLsByScope (aws_metrics.go:1867).
func filterWAFWebACLsByScope(ctx context.Context, client wafv2WebACLsClient, scope wafv2types.Scope, project string) ([]wafv2types.WebACLSummary, error) {
	out, err := client.ListWebACLs(ctx, &wafv2.ListWebACLsInput{Scope: scope})
	if err != nil {
		return nil, fmt.Errorf("wafv2 ListWebACLs scope=%s: %w", scope, err)
	}
	if project == "" {
		return out.WebACLs, nil
	}
	matched := make([]wafv2types.WebACLSummary, 0, len(out.WebACLs))
	for _, w := range out.WebACLs {
		arn := aws.ToString(w.ARN)
		if arn == "" {
			continue
		}
		tagsOut, tagErr := client.ListTagsForResource(ctx, &wafv2.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
		if tagErr != nil {
			log.Printf("[wafv2 ListTagsForResource] skip arn=%s scope=%s: %v", arn, scope, tagErr)
			continue
		}
		if tagsOut.TagInfoForResource != nil {
			for _, t := range tagsOut.TagInfoForResource.TagList {
				if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
					matched = append(matched, w)
					break
				}
			}
		}
	}
	return matched, nil
}

// --- CloudFront ---

// cloudFrontDistributionsClient is the subset of the cloudfront SDK
// used by the filter helper. Mirrors the InsideOut backend's
// cloudFrontDistributionsClient (aws_metrics.go:1045).
type cloudFrontDistributionsClient interface {
	ListDistributions(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error)
	ListTagsForResource(ctx context.Context, params *cloudfront.ListTagsForResourceInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListTagsForResourceOutput, error)
}

func inspectCloudFront(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	// CloudFront is a global service; its API only responds in us-east-1.
	cfg.Region = "us-east-1"
	project := filter.Project(filters)

	switch action {
	case "list-distributions":
		return filterCloudFrontDistributionsByProjectTag(ctx, cloudfront.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("cloudfront")
	default:
		return nil, unsupportedActionError("cloudfront", action)
	}
}

// filterCloudFrontDistributionsByProjectTag paginates ListDistributions
// (NextMarker/Marker) and, when project!="", fans out
// ListTagsForResource per DistributionSummary.ARN.
//
// AWS usually signals end-of-pagination with NextMarker=nil but has been
// observed returning a pointer to an empty string. Treat both as "no
// more pages" to avoid an infinite loop that passes Marker="" back in.
//
// Mirrors the InsideOut backend's filterCloudFrontDistributionsByProjectTag
// (aws_metrics.go:1077).
func filterCloudFrontDistributionsByProjectTag(ctx context.Context, client cloudFrontDistributionsClient, project string) ([]cloudfronttypes.DistributionSummary, error) {
	var all []cloudfronttypes.DistributionSummary
	var marker *string
	for {
		out, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("cloudfront ListDistributions: %w", err)
		}
		if out.DistributionList != nil {
			all = append(all, out.DistributionList.Items...)
			if aws.ToString(out.DistributionList.NextMarker) == "" {
				break
			}
			marker = out.DistributionList.NextMarker
			continue
		}
		break
	}
	if project == "" {
		return all, nil
	}
	matched := make([]cloudfronttypes.DistributionSummary, 0, len(all))
	for _, d := range all {
		arn := aws.ToString(d.ARN)
		if arn == "" {
			continue
		}
		tagsOut, err := client.ListTagsForResource(ctx, &cloudfront.ListTagsForResourceInput{Resource: aws.String(arn)})
		if err != nil {
			log.Printf("[cloudfront ListTagsForResource] skip arn=%s: %v", arn, err)
			continue
		}
		if tagsOut.Tags != nil {
			for _, t := range tagsOut.Tags.Items {
				if aws.ToString(t.Key) == "Project" && aws.ToString(t.Value) == project {
					matched = append(matched, d)
					break
				}
			}
		}
	}
	return matched, nil
}

// --- API Gateway (v2) ---

func inspectAPIGateway(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := apigatewayv2.NewFromConfig(cfg)
	project := filter.Project(filters)

	switch action {
	case "get-apis":
		out, err := client.GetApis(ctx, &apigatewayv2.GetApisInput{})
		if err != nil {
			return nil, err
		}
		// API Gateway v2 inlines Tags as map[string]string.
		if project != "" {
			return filter.Match(toSliceOfMaps(out.Items), project, "Tags", filter.FormatMap), nil
		}
		return out.Items, nil
	case "get-domain-names":
		out, err := client.GetDomainNames(ctx, &apigatewayv2.GetDomainNamesInput{})
		if err != nil {
			return nil, err
		}
		if project != "" {
			return filter.Match(toSliceOfMaps(out.Items), project, "Tags", filter.FormatMap), nil
		}
		return out.Items, nil
	case "get-metrics":
		return metricsRouted("apigateway")
	default:
		return nil, unsupportedActionError("apigateway", action)
	}
}
