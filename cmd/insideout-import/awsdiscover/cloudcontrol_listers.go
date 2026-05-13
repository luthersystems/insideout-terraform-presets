package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

// cognitoUserPoolsLister is the narrow subset of the Cognito IDP SDK
// the Bundle 14b listers use. The interface is package-private so test
// fakes can satisfy it without depending on the full client surface.
type cognitoUserPoolsLister interface {
	ListUserPools(ctx context.Context, in *cognitoidentityprovider.ListUserPoolsInput, opts ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListUserPoolsOutput, error)
	DescribeUserPool(ctx context.Context, in *cognitoidentityprovider.DescribeUserPoolInput, opts ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolOutput, error)
}

// lambdaFunctionsLister is the narrow subset of the Lambda SDK used by
// the Lambda alias parent enumerator.
type lambdaFunctionsLister interface {
	ListFunctions(ctx context.Context, in *lambda.ListFunctionsInput, opts ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
}

// acmCertificatesLister is the narrow subset of the ACM SDK used by the
// ACM certificate SDK enumerator.
type acmCertificatesLister interface {
	ListCertificates(ctx context.Context, in *acm.ListCertificatesInput, opts ...func(*acm.Options)) (*acm.ListCertificatesOutput, error)
}

// apigatewayv2APIsLister is the narrow subset of the API Gateway v2 SDK
// used by the parent-API enumerator that seeds Route / Integration /
// Authorizer fan-out. apigatewayv2_stage.go also lists APIs via its own
// hand-rolled client interface; we intentionally keep these interfaces
// separate so each call site can evolve independently.
type apigatewayv2APIsLister interface {
	GetApis(ctx context.Context, in *apigatewayv2.GetApisInput, opts ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error)
}

// listCognitoUserPools enumerates all Cognito user pools in the region
// and returns one parent ResourceModel JSON string per pool, suitable
// for feeding into Cloud Control ListResources for child types scoped
// on UserPoolId (e.g. AWS::Cognito::UserPoolClient). Returns an empty
// slice (not nil) when no pools exist, so the discoverer's
// `len(parentModels) == 0` early-exit fires cleanly.
func listCognitoUserPools(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := cognitoidentityprovider.NewFromConfig(awsCfg, func(o *cognitoidentityprovider.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listCognitoUserPoolsWithClient(ctx, client)
}

func listCognitoUserPoolsWithClient(ctx context.Context, client cognitoUserPoolsLister) ([]string, error) {
	ids, err := listCognitoUserPoolIDsWithClient(ctx, client)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(ids))
	for _, id := range ids {
		models = append(models, fmt.Sprintf(`{"UserPoolId":%q}`, id))
	}
	return models, nil
}

// listLambdaFunctions enumerates Lambda functions and returns one
// parent ResourceModel JSON string per function for AWS::Lambda::Alias
// fan-out.
func listLambdaFunctions(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := lambda.NewFromConfig(awsCfg, func(o *lambda.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listLambdaFunctionsWithClient(ctx, client)
}

func listLambdaFunctionsWithClient(ctx context.Context, client lambdaFunctionsLister) ([]string, error) {
	models := []string{}
	var marker *string
	for {
		page, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("lambda:ListFunctions: %w", err)
		}
		for _, fn := range page.Functions {
			name := aws.ToString(fn.FunctionName)
			if name == "" {
				continue
			}
			models = append(models, fmt.Sprintf(`{"FunctionName":%q}`, name))
		}
		if page.NextMarker == nil || aws.ToString(page.NextMarker) == "" {
			break
		}
		marker = page.NextMarker
	}
	return models, nil
}

// wafv2ParentModels returns the static WAFv2 parent ResourceModel list.
// CLOUDFRONT scope is only valid against the us-east-1 endpoint per the
// AWS WAFv2 docs; from any other region we surface REGIONAL only.
// Returning the CLOUDFRONT scope from non-us-east-1 would cause the
// downstream CC ListResources call to return InvalidRequestException.
//
// Emit order is REGIONAL first, then CLOUDFRONT — the discoverer
// preserves the order for emit determinism, so tests pin this.
func wafv2ParentModels(_ context.Context, _ aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	if region == "us-east-1" {
		return []string{
			`{"Scope":"REGIONAL"}`,
			`{"Scope":"CLOUDFRONT"}`,
		}, nil
	}
	return []string{`{"Scope":"REGIONAL"}`}, nil
}

// listCognitoUserPoolDomains walks user pools and emits the compound
// `<UserPoolId>|<Domain>` Cloud Control primary identifier for each
// pool that has Domain (Cognito-hosted) or CustomDomain (customer DNS)
// configured. CFN treats Domain and CustomDomain as separate
// AWS::Cognito::UserPoolDomain resources so both are emitted when both
// are present.
//
// IMPORTANT: AWS::Cognito::UserPoolDomain's CC primary identifier is
// the **compound** `<UserPoolId>|<Domain>` (per its CFN schema's
// `primaryIdentifier: [/properties/UserPoolId, /properties/Domain]`),
// NOT the bare Domain string. Emitting bare Domain causes CC
// GetResource to return:
//
//	ValidationException: Identifier <X> is not valid for identifier
//	[/properties/UserPoolId, /properties/Domain]
//
// This was the #421 regression — caught by the post-merge live smoke
// of #412. The TF import format for aws_cognito_user_pool_domain is
// the bare Domain, so the per-type ImportIDFromIdentifier strips the
// `<UserPoolId>|` prefix before handing off to the Terraform
// importer.
func listCognitoUserPoolDomains(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := cognitoidentityprovider.NewFromConfig(awsCfg, func(o *cognitoidentityprovider.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listCognitoUserPoolDomainsWithClient(ctx, client)
}

func listCognitoUserPoolDomainsWithClient(ctx context.Context, client cognitoUserPoolsLister) ([]string, error) {
	pools, err := listCognitoUserPoolIDsWithClient(ctx, client)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, poolID := range pools {
		out, err := client.DescribeUserPool(ctx, &cognitoidentityprovider.DescribeUserPoolInput{
			UserPoolId: aws.String(poolID),
		})
		if err != nil {
			return nil, fmt.Errorf("cognito-idp:DescribeUserPool %s: %w", poolID, err)
		}
		if out == nil || out.UserPool == nil {
			continue
		}
		if d := aws.ToString(out.UserPool.Domain); d != "" {
			ids = append(ids, poolID+"|"+d)
		}
		if cd := aws.ToString(out.UserPool.CustomDomain); cd != "" {
			ids = append(ids, poolID+"|"+cd)
		}
	}
	return ids, nil
}

// listCognitoUserPoolIDsWithClient is a small helper shared by the
// pool-walking listers. Returns pool IDs only, not ResourceModel JSON.
func listCognitoUserPoolIDsWithClient(ctx context.Context, client cognitoUserPoolsLister) ([]string, error) {
	ids := []string{}
	var nextToken *string
	for {
		page, err := client.ListUserPools(ctx, &cognitoidentityprovider.ListUserPoolsInput{
			MaxResults: aws.Int32(60),
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("cognito-idp:ListUserPools: %w", err)
		}
		for _, p := range page.UserPools {
			id := aws.ToString(p.Id)
			if id != "" {
				ids = append(ids, id)
			}
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return ids, nil
}

// listACMCertificates returns the certificate ARN for every ACM
// certificate in the region. ARN is the CC primary identifier for
// AWS::CertificateManager::Certificate.
func listACMCertificates(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := acm.NewFromConfig(awsCfg, func(o *acm.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listACMCertificatesWithClient(ctx, client)
}

func listACMCertificatesWithClient(ctx context.Context, client acmCertificatesLister) ([]string, error) {
	arns := []string{}
	var nextToken *string
	for {
		page, err := client.ListCertificates(ctx, &acm.ListCertificatesInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("acm:ListCertificates: %w", err)
		}
		for _, c := range page.CertificateSummaryList {
			a := aws.ToString(c.CertificateArn)
			if a != "" {
				arns = append(arns, a)
			}
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return arns, nil
}

// listApigatewayv2Apis enumerates ApiGatewayV2 APIs in the region and
// returns one parent ResourceModel JSON string per API, suitable for
// feeding into Cloud Control ListResources for child types scoped on
// ApiId (Route / Integration / Authorizer). Returns an empty slice (not
// nil) when no APIs exist, so the discoverer's `len(parentModels) == 0`
// early-exit fires cleanly.
func listApigatewayv2Apis(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := apigatewayv2.NewFromConfig(awsCfg, func(o *apigatewayv2.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listApigatewayv2ApisWithClient(ctx, client)
}

func listApigatewayv2ApisWithClient(ctx context.Context, client apigatewayv2APIsLister) ([]string, error) {
	models := []string{}
	var nextToken *string
	for {
		page, err := client.GetApis(ctx, &apigatewayv2.GetApisInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("apigatewayv2:GetApis: %w", err)
		}
		for _, api := range page.Items {
			id := aws.ToString(api.ApiId)
			if id == "" {
				continue
			}
			models = append(models, fmt.Sprintf(`{"ApiId":%q}`, id))
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return models, nil
}

