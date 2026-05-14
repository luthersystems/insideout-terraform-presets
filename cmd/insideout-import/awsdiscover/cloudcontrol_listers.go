package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
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

// apigatewayRestAPIsLister is the narrow subset of the API Gateway v1
// SDK used by the parent-RestApi enumerator that seeds Stage /
// Deployment / Resource fan-out (#422). The v1 service uses `Position`
// as the pagination cursor (not `NextToken`), so this interface lives
// alongside but separate from apigatewayv2APIsLister.
type apigatewayRestAPIsLister interface {
	GetRestApis(ctx context.Context, in *apigateway.GetRestApisInput, opts ...func(*apigateway.Options)) (*apigateway.GetRestApisOutput, error)
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

// listApigatewayRestAPIs enumerates API Gateway v1 (REST) APIs in the
// region and returns one parent ResourceModel JSON string per API,
// suitable for feeding into Cloud Control ListResources for child types
// scoped on RestApiId (Stage / Deployment / Resource — #422). Returns
// an empty slice (not nil) when no APIs exist, so the discoverer's
// `len(parentModels) == 0` early-exit fires cleanly.
//
// API Gateway v1 paginates via `Position` (string cursor), not
// `NextToken`, so the pagination loop here uses Position. The
// terminator condition mirrors listApigatewayv2Apis: stop on both nil
// AND empty-string cursors, since some SDK responses return `&""`
// instead of nil on the final page.
func listApigatewayRestAPIs(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := apigateway.NewFromConfig(awsCfg, func(o *apigateway.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listApigatewayRestAPIsWithClient(ctx, client)
}

func listApigatewayRestAPIsWithClient(ctx context.Context, client apigatewayRestAPIsLister) ([]string, error) {
	models := []string{}
	var position *string
	for {
		page, err := client.GetRestApis(ctx, &apigateway.GetRestApisInput{Position: position})
		if err != nil {
			return nil, fmt.Errorf("apigateway:GetRestApis: %w", err)
		}
		for _, api := range page.Items {
			id := aws.ToString(api.Id)
			if id == "" {
				continue
			}
			models = append(models, fmt.Sprintf(`{"RestApiId":%q}`, id))
		}
		if page.Position == nil || aws.ToString(page.Position) == "" {
			break
		}
		position = page.Position
	}
	return models, nil
}

// listLambdaFunctionArns enumerates Lambda functions and returns one
// parent ResourceModel JSON string per function, keyed under
// `TargetFunctionArn` (the field name expected by
// AWS::Lambda::Url's CC list-handler schema — #422). Distinct from
// listLambdaFunctions, which emits `{"FunctionName":"..."}` for types
// whose CC list-handler keys on FunctionName (e.g. AWS::Lambda::Alias,
// AWS::Lambda::Permission). Reuses the lambdaFunctionsLister interface
// (same SDK call, ListFunctions; different ResourceModel emission).
func listLambdaFunctionArns(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := lambda.NewFromConfig(awsCfg, func(o *lambda.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listLambdaFunctionArnsWithClient(ctx, client)
}

func listLambdaFunctionArnsWithClient(ctx context.Context, client lambdaFunctionsLister) ([]string, error) {
	models := []string{}
	var marker *string
	for {
		page, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("lambda:ListFunctions: %w", err)
		}
		for _, fn := range page.Functions {
			arn := aws.ToString(fn.FunctionArn)
			if arn == "" {
				continue
			}
			models = append(models, fmt.Sprintf(`{"TargetFunctionArn":%q}`, arn))
		}
		if page.NextMarker == nil || aws.ToString(page.NextMarker) == "" {
			break
		}
		marker = page.NextMarker
	}
	return models, nil
}

// kmsAliasesLister is the narrow subset of the KMS SDK used by the
// aws_kms_alias SDKLister enumerator (#430). The interface is
// package-private so test fakes can satisfy it without depending on the
// full KMS client surface.
type kmsAliasesLister interface {
	ListAliases(ctx context.Context, in *kms.ListAliasesInput, opts ...func(*kms.Options)) (*kms.ListAliasesOutput, error)
}

// listKMSAliases enumerates KMS aliases in the region and returns the
// alias name (e.g. "alias/foo") for each. AliasName is the CC primary
// identifier for AWS::KMS::Alias and is also Terraform's import format —
// passthrough.
//
// KMS list paginates via `NextMarker` (string cursor; the input field is
// `Marker`, not `NextToken`). Like the other listers in this file, we
// stop the loop on both nil AND empty-string cursors so SDK responses
// that return `&""` on the final page don't loop forever.
func listKMSAliases(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := kms.NewFromConfig(awsCfg, func(o *kms.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listKMSAliasesWithClient(ctx, client)
}

func listKMSAliasesWithClient(ctx context.Context, client kmsAliasesLister) ([]string, error) {
	names := []string{}
	var marker *string
	for {
		page, err := client.ListAliases(ctx, &kms.ListAliasesInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("kms:ListAliases: %w", err)
		}
		for _, a := range page.Aliases {
			name := aws.ToString(a.AliasName)
			if name == "" {
				continue
			}
			names = append(names, name)
		}
		if page.NextMarker == nil || aws.ToString(page.NextMarker) == "" {
			break
		}
		marker = page.NextMarker
	}
	return names, nil
}

// iamUsersLister is the narrow subset of the IAM SDK used by the
// aws_iam_user SDKLister enumerator (#430).
type iamUsersLister interface {
	ListUsers(ctx context.Context, in *iam.ListUsersInput, opts ...func(*iam.Options)) (*iam.ListUsersOutput, error)
}

// listIAMUsers enumerates IAM users (global service — region is ignored
// by the SDK for IAM) and returns the UserName of each. UserName is the
// CC primary identifier for AWS::IAM::User and is also Terraform's
// import format — passthrough.
//
// IAM list paginates via `Marker` (string cursor). The IsTruncated flag
// signals more pages; we still defend the loop terminator by also
// breaking when Marker is nil or empty (parity with the other listers).
func listIAMUsers(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := iam.NewFromConfig(awsCfg, func(o *iam.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listIAMUsersWithClient(ctx, client)
}

func listIAMUsersWithClient(ctx context.Context, client iamUsersLister) ([]string, error) {
	names := []string{}
	var marker *string
	for {
		page, err := client.ListUsers(ctx, &iam.ListUsersInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("iam:ListUsers: %w", err)
		}
		for _, u := range page.Users {
			n := aws.ToString(u.UserName)
			if n == "" {
				continue
			}
			names = append(names, n)
		}
		if !page.IsTruncated || page.Marker == nil || aws.ToString(page.Marker) == "" {
			break
		}
		marker = page.Marker
	}
	return names, nil
}

// iamGroupsLister is the narrow subset of the IAM SDK used by the
// aws_iam_group SDKLister enumerator (#430).
type iamGroupsLister interface {
	ListGroups(ctx context.Context, in *iam.ListGroupsInput, opts ...func(*iam.Options)) (*iam.ListGroupsOutput, error)
}

// listIAMGroups enumerates IAM groups (global service) and returns the
// GroupName of each. GroupName is the CC primary identifier for
// AWS::IAM::Group and Terraform's import format — passthrough.
func listIAMGroups(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := iam.NewFromConfig(awsCfg, func(o *iam.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listIAMGroupsWithClient(ctx, client)
}

func listIAMGroupsWithClient(ctx context.Context, client iamGroupsLister) ([]string, error) {
	names := []string{}
	var marker *string
	for {
		page, err := client.ListGroups(ctx, &iam.ListGroupsInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("iam:ListGroups: %w", err)
		}
		for _, g := range page.Groups {
			n := aws.ToString(g.GroupName)
			if n == "" {
				continue
			}
			names = append(names, n)
		}
		if !page.IsTruncated || page.Marker == nil || aws.ToString(page.Marker) == "" {
			break
		}
		marker = page.Marker
	}
	return names, nil
}

// cloudfrontFunctionsLister is the narrow subset of the CloudFront SDK
// used by the aws_cloudfront_function SDKLister enumerator (#430).
type cloudfrontFunctionsLister interface {
	ListFunctions(ctx context.Context, in *cloudfront.ListFunctionsInput, opts ...func(*cloudfront.Options)) (*cloudfront.ListFunctionsOutput, error)
}

// listCloudFrontFunctions enumerates CloudFront functions (global
// service) and returns the FunctionARN for each. The CC primary
// identifier for AWS::CloudFront::Function is FunctionARN; Terraform's
// import format is the bare function NAME (CC vs TF divergence). The
// per-type config's ImportIDFromIdentifier rewrites the ARN tail into a
// name before handing to the importer.
//
// CloudFront list paginates via `Marker` (string cursor; the response
// field is also `Marker` on the next-page-marker NextMarker). The
// terminator condition mirrors the other listers: break on both nil and
// empty-string cursors.
func listCloudFrontFunctions(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := cloudfront.NewFromConfig(awsCfg, func(o *cloudfront.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listCloudFrontFunctionsWithClient(ctx, client)
}

func listCloudFrontFunctionsWithClient(ctx context.Context, client cloudfrontFunctionsLister) ([]string, error) {
	arns := []string{}
	var marker *string
	for {
		page, err := client.ListFunctions(ctx, &cloudfront.ListFunctionsInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("cloudfront:ListFunctions: %w", err)
		}
		if page.FunctionList != nil {
			for _, fn := range page.FunctionList.Items {
				arn := ""
				if fn.FunctionMetadata != nil {
					arn = aws.ToString(fn.FunctionMetadata.FunctionARN)
				}
				if arn == "" {
					continue
				}
				arns = append(arns, arn)
			}
		}
		next := ""
		if page.FunctionList != nil {
			next = aws.ToString(page.FunctionList.NextMarker)
		}
		if next == "" {
			break
		}
		marker = aws.String(next)
	}
	return arns, nil
}

// secretsManagerSecretsLister is the narrow subset of the Secrets
// Manager SDK used by the aws_secretsmanager_secret_rotation SDKLister
// enumerator (#430). The lister enumerates secrets and filters to those
// with rotation enabled — rotation is a per-secret CFN sub-resource
// (AWS::SecretsManager::RotationSchedule) whose primary identifier is
// the parent secret's ARN.
type secretsManagerSecretsLister interface {
	ListSecrets(ctx context.Context, in *secretsmanager.ListSecretsInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
}

// listSecretsManagerSecretRotations enumerates Secrets Manager secrets
// that have rotation enabled and returns the secret ARN for each. ARN
// is the CC primary identifier (`Id` property) for
// AWS::SecretsManager::RotationSchedule, and is also Terraform's import
// format for aws_secretsmanager_secret_rotation — passthrough.
//
// Secrets without rotation enabled are skipped client-side: emitting
// their ARNs would cause CC GetResource on the RotationSchedule sub-
// resource to surface ResourceNotFoundException for every non-rotated
// secret. ListSecrets.SecretList.RotationEnabled is server-populated so
// no second SDK call is needed.
//
// ListSecrets paginates via `NextToken` (string cursor).
func listSecretsManagerSecretRotations(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := secretsmanager.NewFromConfig(awsCfg, func(o *secretsmanager.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listSecretsManagerSecretRotationsWithClient(ctx, client)
}

func listSecretsManagerSecretRotationsWithClient(ctx context.Context, client secretsManagerSecretsLister) ([]string, error) {
	arns := []string{}
	var nextToken *string
	for {
		page, err := client.ListSecrets(ctx, &secretsmanager.ListSecretsInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("secretsmanager:ListSecrets: %w", err)
		}
		for _, s := range page.SecretList {
			if !aws.ToBool(s.RotationEnabled) {
				continue
			}
			arn := aws.ToString(s.ARN)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return arns, nil
}

