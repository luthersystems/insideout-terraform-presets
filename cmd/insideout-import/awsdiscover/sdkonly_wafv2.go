package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	wafv2types "github.com/aws/aws-sdk-go-v2/service/wafv2/types"
)

// wafv2AssociationClient is the narrow subset of the WAFv2 API the
// aws_wafv2_web_acl_association SDK-only sub-resource discoverer
// issues. Real *wafv2.Client and in-test fakes satisfy this interface;
// production code constructs the real client via wafv2.NewFromConfig
// from each ListParents / FetchItems closure (factory at
// newWAFv2AssociationClient).
type wafv2AssociationClient interface {
	ListWebACLs(ctx context.Context, in *wafv2.ListWebACLsInput, opts ...func(*wafv2.Options)) (*wafv2.ListWebACLsOutput, error)
	ListResourcesForWebACL(ctx context.Context, in *wafv2.ListResourcesForWebACLInput, opts ...func(*wafv2.Options)) (*wafv2.ListResourcesForWebACLOutput, error)
}

// newWAFv2AssociationClient is the production factory injected into
// each WAFv2 FetchItems / ListParents closure. The region argument
// pins the endpoint — CLOUDFRONT scope is only valid against us-east-1
// per the WAFv2 docs (the discoverer's per-scope dispatch enforces
// this).
var newWAFv2AssociationClient = func(awsCfg aws.Config, region string) wafv2AssociationClient {
	return wafv2.NewFromConfig(awsCfg, func(o *wafv2.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// wafv2RegionalAssociationResourceTypes is the set of regional
// resource types that ListResourcesForWebACL can enumerate. ALB and
// API Gateway have been on the API since launch; App Runner +
// Cognito user pool + Verified Access were added more recently.
//
// CLOUDFRONT distributions are NOT listed here because the WAFv2 API
// for CloudFront associations uses a different call shape (via
// ListDistributionsByWebACLId on the cloudfront service) and only
// works against the us-east-1 endpoint — handled in
// wafv2EnumerateAssociations below as a separate branch.
//
// The set is exported as a package-level var (not a function literal
// or constant slice) so tests can introspect / override it; consumers
// must treat it as read-only.
var wafv2RegionalAssociationResourceTypes = []wafv2types.ResourceType{
	wafv2types.ResourceTypeApplicationLoadBalancer,
	wafv2types.ResourceTypeApiGateway,
	wafv2types.ResourceTypeAppsync,
	wafv2types.ResourceTypeCognitioUserPool,
	wafv2types.ResourceTypeAppRunnerService,
	wafv2types.ResourceTypeVerifiedAccessInstance,
	wafv2types.ResourceTypeAmplify,
}

// listWAFv2WebACLs enumerates every WAFv2 Web ACL in scope (REGIONAL
// in any region; CLOUDFRONT only in us-east-1 per the WAFv2 docs) and
// returns one parent identifier per ACL as "<arn>".
//
// Used as the ListParents callback for
// aws_wafv2_web_acl_association. The ARN carries enough information
// (scope, region, ACL id) for FetchItems to dispatch the per-resource-
// type ListResourcesForWebACL calls without a separate lookup.
//
// Emit order is deterministic per AWS's pagination: WAFv2 returns
// ACLs in creation order. The downstream framework re-sorts results
// by ImportID, so any concurrent emission ordering does not affect
// final output.
//
// Returns a non-nil empty slice on accounts with zero ACLs (#255).
func listWAFv2WebACLs(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := newWAFv2AssociationClient(awsCfg, region)
	return listWAFv2WebACLsWithClient(ctx, client, region)
}

func listWAFv2WebACLsWithClient(ctx context.Context, client wafv2AssociationClient, region string) ([]string, error) {
	arns := []string{}
	scopes := []wafv2types.Scope{wafv2types.ScopeRegional}
	if region == "us-east-1" {
		scopes = append(scopes, wafv2types.ScopeCloudfront)
	}
	for _, scope := range scopes {
		var marker *string
		for {
			page, err := client.ListWebACLs(ctx, &wafv2.ListWebACLsInput{Scope: scope, NextMarker: marker})
			if err != nil {
				return nil, fmt.Errorf("wafv2:ListWebACLs scope=%s: %w", scope, err)
			}
			for _, acl := range page.WebACLs {
				arn := aws.ToString(acl.ARN)
				if arn == "" {
					continue
				}
				arns = append(arns, arn)
			}
			if page.NextMarker == nil || aws.ToString(page.NextMarker) == "" {
				break
			}
			marker = page.NextMarker
		}
	}
	return arns, nil
}

// fetchWAFv2WebACLAssociations implements FetchItems for
// aws_wafv2_web_acl_association.
//
// One parent (WebACL ARN) yields N emissions — one per associated
// resource across the regional resource-type matrix
// (ALB, API Gateway, AppSync, Cognito UserPool, AppRunner Service,
// Verified Access Instance). The per-resource-type fan-out is
// deliberate: ListResourcesForWebACL only returns one type at a time
// (driven by the input's ResourceType field).
//
// Terraform's import format for aws_wafv2_web_acl_association is
// "<resource_arn>,<web_acl_arn>" — comma-delimited, resource first.
// Verified against terraform-provider-aws v6.x
// internal/service/wafv2/web_acl_association.go::resourceWebACLAssociationImport,
// which splits on "," with N=2 and assigns parts[0]=resourceArn,
// parts[1]=webACLArn.
//
// CLOUDFRONT scope: the framework already filters ACL enumeration to
// us-east-1 only for CLOUDFRONT (see listWAFv2WebACLs); FetchItems
// here doesn't need a separate CloudFront branch because the WAFv2
// API doesn't expose CloudFront associations via
// ListResourcesForWebACL — they live on the CloudFront distribution's
// WebACLId property and surface via the distribution's CC handler.
// The CLOUDFRONT-scoped Web ACL therefore emits zero associations on
// this path (the upstream aws_cloudfront_distribution discoverer
// already captures the WebACLId binding).
//
// Per-resource-type errors (e.g. WAFInvalidParameterException for
// resource types not supported in this region) are converted to
// zero emissions for that type rather than aborting the parent —
// matches the per-item soft-fail posture on other multi-emit
// pipelines.
func fetchWAFv2WebACLAssociations(ctx context.Context, awsCfg aws.Config, region, webACLArn string) ([]subresourceEmission, error) {
	return fetchWAFv2WebACLAssociationsWithClient(ctx, newWAFv2AssociationClient(awsCfg, region), webACLArn)
}

func fetchWAFv2WebACLAssociationsWithClient(ctx context.Context, client wafv2AssociationClient, webACLArn string) ([]subresourceEmission, error) {
	emissions := []subresourceEmission{}
	for _, rt := range wafv2RegionalAssociationResourceTypes {
		out, err := client.ListResourcesForWebACL(ctx, &wafv2.ListResourcesForWebACLInput{
			WebACLArn:    aws.String(webACLArn),
			ResourceType: rt,
		})
		if err != nil {
			// WAFInvalidParameterException is what WAFv2 surfaces when
			// the resource type isn't available in the current region
			// (e.g. Verified Access in a region without the service).
			// Swallow per-type errors to keep the per-WebACL fan-out
			// soft-failing per-type rather than per-WebACL.
			if isAPIErrorCode(err, "WAFInvalidParameterException", "ValidationException") {
				continue
			}
			return nil, fmt.Errorf("wafv2:ListResourcesForWebACL webacl=%q resourceType=%s: %w", webACLArn, rt, err)
		}
		for _, resourceARN := range out.ResourceArns {
			if resourceARN == "" {
				continue
			}
			emissions = append(emissions, subresourceEmission{
				ImportID: resourceARN + "," + webACLArn,
				NameHint: resourceARN,
				NativeIDs: map[string]string{
					"resource_arn": resourceARN,
					"web_acl_arn":  webACLArn,
				},
				Props: map[string]any{
					"WebACLArn":    webACLArn,
					"ResourceArn":  resourceARN,
					"ResourceType": string(rt),
				},
			})
		}
	}
	return emissions, nil
}
