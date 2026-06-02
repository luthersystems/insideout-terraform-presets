// Auth-tier AWS service inspector: Cognito user pools.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect.go (cognito:959)
// plus the helper in aws_metrics.go
// (filterCognitoUserPoolsByProjectTag:1405).
//
// Cognito's tag-discovery dance is awkward — ListUserPools returns
// neither an ARN nor inline tags, so we pivot through DescribeUserPool
// to get the ARN before calling ListTagsForResource. Per-pool errors
// log+skip (fail-closed) — Cognito is throttle-sensitive so one
// TooManyRequestsException shouldn't wipe the whole result.

package aws

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability/filter"
)

// cognitoUserPoolsClient is the subset of the cognito-idp SDK used by
// the user-pool filter helper. Mirrors the InsideOut backend's cognitoUserPoolsClient
// (aws_metrics.go:1380).
type cognitoUserPoolsClient interface {
	ListUserPools(ctx context.Context, params *cognitoidentityprovider.ListUserPoolsInput, optFns ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListUserPoolsOutput, error)
	DescribeUserPool(ctx context.Context, params *cognitoidentityprovider.DescribeUserPoolInput, optFns ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolOutput, error)
	ListTagsForResource(ctx context.Context, params *cognitoidentityprovider.ListTagsForResourceInput, optFns ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListTagsForResourceOutput, error)
}

// userPoolWithMFA is the enriched element the project-scoped filter path
// returns: the ListUserPools summary extended with the pool's
// MfaConfiguration (OFF|ON|OPTIONAL). The scope filter already calls
// DescribeUserPool to resolve the pool ARN, so MfaConfiguration comes off
// that same response — NO extra SDK round-trip (the same "data already
// fetched, just surface it" enrichment as the VPC IGW join).
//
// MfaConfiguration is a *string so the JSON envelope OMITS it when the
// DescribeUserPool data wasn't available (the empty-project demo path,
// which skips DescribeUserPool entirely), letting extractCognitoConfig tell
// "MFA genuinely off" apart from "not fetched" — the same presence check
// extractVPCConfig does for HasInternetGateway (#712).
type userPoolWithMFA struct {
	cognitoidptypes.UserPoolDescriptionType
	MfaConfiguration *string `json:"MfaConfiguration,omitempty"`
}

func inspectCognito(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	project := filter.Project(filters)
	switch action {
	case "list-user-pools":
		return filterCognitoUserPoolsByProjectTag(ctx, cognitoidentityprovider.NewFromConfig(cfg), project)
	case "get-metrics":
		return metricsRouted("cognito")
	default:
		return nil, unsupportedActionError("cognito", action)
	}
}

// filterCognitoUserPoolsByProjectTag paginates ListUserPools, then for
// each pool calls DescribeUserPool → Arn → ListTagsForResource(Arn) to
// check the Project tag. Per-pool errors are log-skipped — common
// causes are concurrent delete and Cognito's throttle-prone
// TooManyRequestsException.
//
// Mirrors the InsideOut backend's filterCognitoUserPoolsByProjectTag
// (aws_metrics.go:1405).
//
// Project-scoped matches are enriched with MfaConfiguration off the
// DescribeUserPool response the scope filter already issues, so
// extractCognitoConfig can surface aws_cognito.mfaRequired with no extra
// SDK call (#712). The empty-project demo path skips DescribeUserPool and
// returns pools with MfaConfiguration unset (omitted from the envelope).
func filterCognitoUserPoolsByProjectTag(ctx context.Context, client cognitoUserPoolsClient, project string) ([]userPoolWithMFA, error) {
	all := []cognitoidptypes.UserPoolDescriptionType{}
	paginator := cognitoidentityprovider.NewListUserPoolsPaginator(client, &cognitoidentityprovider.ListUserPoolsInput{
		// 60 is the ListUserPools MaxResults API max — minimises
		// round-trips against Cognito's throttle ceiling.
		MaxResults: aws.Int32(60),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("cognito-idp ListUserPools: %w", err)
		}
		all = append(all, page.UserPools...)
	}
	if project == "" {
		// Demo-session fallback: skip the per-pool DescribeUserPool scope
		// filter. MfaConfiguration stays unset (omitted) since we don't
		// fetch the describe response here.
		out := make([]userPoolWithMFA, 0, len(all))
		for _, p := range all {
			out = append(out, userPoolWithMFA{UserPoolDescriptionType: p})
		}
		return out, nil
	}
	matched := make([]userPoolWithMFA, 0, len(all))
	for _, p := range all {
		id := aws.ToString(p.Id)
		if id == "" {
			continue
		}
		descOut, err := client.DescribeUserPool(ctx, &cognitoidentityprovider.DescribeUserPoolInput{UserPoolId: aws.String(id)})
		if err != nil {
			log.Printf("[cognito-idp DescribeUserPool] skip pool=%s: %v", id, err)
			continue
		}
		if descOut.UserPool == nil {
			continue
		}
		arn := aws.ToString(descOut.UserPool.Arn)
		if arn == "" {
			continue
		}
		tagsOut, err := client.ListTagsForResource(ctx, &cognitoidentityprovider.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
		if err != nil {
			log.Printf("[cognito-idp ListTagsForResource] skip arn=%s: %v", arn, err)
			continue
		}
		if tagsOut.Tags["Project"] == project {
			wrapped := userPoolWithMFA{UserPoolDescriptionType: p}
			// MfaConfiguration is already on the describe response we just
			// fetched for the ARN — surface it for free.
			if mfa := string(descOut.UserPool.MfaConfiguration); mfa != "" {
				wrapped.MfaConfiguration = aws.String(mfa)
			}
			matched = append(matched, wrapped)
		}
	}
	return matched, nil
}
