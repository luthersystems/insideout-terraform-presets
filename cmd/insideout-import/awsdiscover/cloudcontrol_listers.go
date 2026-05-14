package awsdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
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

// eksClustersLister is the narrow subset of the EKS SDK used by the
// aws_eks_cluster SDKLister enumerator and the four EKS child types
// (Nodegroup, Addon, FargateProfile, AccessEntry) whose ParentLister
// fans out per ClusterName (#14f). The interface is package-private so
// test fakes can satisfy it without depending on the full EKS client.
type eksClustersLister interface {
	ListClusters(ctx context.Context, in *eks.ListClustersInput, opts ...func(*eks.Options)) (*eks.ListClustersOutput, error)
}

// listEKSClusters enumerates EKS clusters in the region and returns
// cluster Name for each. Name is the CC primary identifier for
// AWS::EKS::Cluster (and Terraform's import format for
// aws_eks_cluster — passthrough).
//
// EKS ListClusters paginates via NextToken. The terminator condition
// mirrors the other listers: break on both nil AND empty-string cursors.
func listEKSClusters(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := eks.NewFromConfig(awsCfg, func(o *eks.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listEKSClustersWithClient(ctx, client)
}

func listEKSClustersWithClient(ctx context.Context, client eksClustersLister) ([]string, error) {
	names := []string{}
	var nextToken *string
	for {
		page, err := client.ListClusters(ctx, &eks.ListClustersInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("eks:ListClusters: %w", err)
		}
		for _, name := range page.Clusters {
			if name == "" {
				continue
			}
			names = append(names, name)
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return names, nil
}

// listEKSClustersAsResourceModels enumerates EKS clusters and wraps
// each cluster name into a JSON ResourceModel `{"ClusterName":"..."}`
// for feeding into Cloud Control ListResources for the four child
// types scoped on ClusterName: Nodegroup, Addon, FargateProfile,
// AccessEntry (#14f). Reuses `listEKSClusters` for the underlying SDK
// call so pagination semantics are shared.
func listEKSClustersAsResourceModels(ctx context.Context, awsCfg aws.Config, region string, args DiscoverArgs) ([]string, error) {
	names, err := listEKSClusters(ctx, awsCfg, region, args)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(names))
	for _, n := range names {
		models = append(models, fmt.Sprintf(`{"ClusterName":%q}`, n))
	}
	return models, nil
}

// ec2InstancesLister is the narrow subset of the EC2 SDK used by the
// aws_instance SDKLister enumerator (#14f).
type ec2InstancesLister interface {
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// listEC2Instances enumerates EC2 instances in the region and returns
// the InstanceId for each instance that is not in a terminated or
// shutting-down state. Those tombstone states are skipped because the
// downstream CC GetResource fan-out would surface
// ResourceNotFoundException for every terminated instance, polluting
// the soft-fail warn channel for what is effectively dead inventory.
// InstanceId is the CC primary identifier for AWS::EC2::Instance and
// Terraform's import format — passthrough.
//
// DescribeInstances paginates via NextToken and returns instances
// grouped under Reservations.
func listEC2Instances(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := ec2.NewFromConfig(awsCfg, func(o *ec2.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listEC2InstancesWithClient(ctx, client)
}

func listEC2InstancesWithClient(ctx context.Context, client ec2InstancesLister) ([]string, error) {
	ids := []string{}
	var nextToken *string
	for {
		page, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("ec2:DescribeInstances: %w", err)
		}
		for _, res := range page.Reservations {
			for _, inst := range res.Instances {
				if inst.State != nil {
					switch inst.State.Name {
					case ec2types.InstanceStateNameTerminated, ec2types.InstanceStateNameShuttingDown:
						continue
					}
				}
				id := aws.ToString(inst.InstanceId)
				if id == "" {
					continue
				}
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

// ec2KeyPairsLister is the narrow subset of the EC2 SDK used by the
// aws_key_pair SDKLister enumerator (#14f).
type ec2KeyPairsLister interface {
	DescribeKeyPairs(ctx context.Context, in *ec2.DescribeKeyPairsInput, opts ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error)
}

// listEC2KeyPairs enumerates EC2 key pairs in the region and returns
// the KeyName for each. KeyName is the CC primary identifier for
// AWS::EC2::KeyPair and Terraform's import format — passthrough.
//
// DescribeKeyPairs does not paginate (per-account key-pair counts are
// bounded by AWS service quotas and the SDK returns the full list in
// a single response).
func listEC2KeyPairs(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := ec2.NewFromConfig(awsCfg, func(o *ec2.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listEC2KeyPairsWithClient(ctx, client)
}

func listEC2KeyPairsWithClient(ctx context.Context, client ec2KeyPairsLister) ([]string, error) {
	names := []string{}
	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		return nil, fmt.Errorf("ec2:DescribeKeyPairs: %w", err)
	}
	for _, kp := range out.KeyPairs {
		n := aws.ToString(kp.KeyName)
		if n == "" {
			continue
		}
		names = append(names, n)
	}
	return names, nil
}

// autoScalingGroupsLister is the narrow subset of the AutoScaling SDK
// used by the aws_autoscaling_group SDKLister enumerator (#14f).
type autoScalingGroupsLister interface {
	DescribeAutoScalingGroups(ctx context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, opts ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

// listAutoScalingGroups enumerates Auto Scaling groups in the region
// and returns AutoScalingGroupName for each. The name is the CC
// primary identifier for AWS::AutoScaling::AutoScalingGroup and
// Terraform's import format — passthrough.
//
// DescribeAutoScalingGroups paginates via NextToken.
func listAutoScalingGroups(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := autoscaling.NewFromConfig(awsCfg, func(o *autoscaling.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listAutoScalingGroupsWithClient(ctx, client)
}

func listAutoScalingGroupsWithClient(ctx context.Context, client autoScalingGroupsLister) ([]string, error) {
	names := []string{}
	var nextToken *string
	for {
		page, err := client.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("autoscaling:DescribeAutoScalingGroups: %w", err)
		}
		for _, g := range page.AutoScalingGroups {
			n := aws.ToString(g.AutoScalingGroupName)
			if n == "" {
				continue
			}
			names = append(names, n)
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return names, nil
}

// openSearchDomainsLister is the narrow subset of the OpenSearch SDK
// used by the aws_opensearch_domain SDKLister enumerator (#14g).
// AWS::OpenSearchService::Domain's CC ListResources returns
// UnsupportedActionException even though CC GetResource works, so
// enumeration goes through the native opensearch:ListDomainNames API.
// The interface is package-private so test fakes can satisfy it without
// depending on the full OpenSearch client.
type openSearchDomainsLister interface {
	ListDomainNames(ctx context.Context, in *opensearch.ListDomainNamesInput, opts ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error)
}

// listOpenSearchDomains enumerates OpenSearch (and Elasticsearch
// engine-type) domains in the region and returns the DomainName for
// each. DomainName is the CC primary identifier for
// AWS::OpenSearchService::Domain (and Terraform's import format for
// aws_opensearch_domain — passthrough).
//
// opensearch:ListDomainNames is non-paginated (single response, all
// domains in the region) so there is no NextToken loop.
func listOpenSearchDomains(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := opensearch.NewFromConfig(awsCfg, func(o *opensearch.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listOpenSearchDomainsWithClient(ctx, client)
}

func listOpenSearchDomainsWithClient(ctx context.Context, client openSearchDomainsLister) ([]string, error) {
	out, err := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if err != nil {
		return nil, fmt.Errorf("opensearch:ListDomainNames: %w", err)
	}
	names := []string{}
	for _, d := range out.DomainNames {
		n := aws.ToString(d.DomainName)
		if n == "" {
			continue
		}
		names = append(names, n)
	}
	return names, nil
}

// cloudFrontDistributionsLister is the narrow subset of the CloudFront
// SDK used by the aws_cloudfront_monitoring_subscription SDKLister
// enumerator (#14h). MonitoringSubscription is a per-distribution
// sub-resource keyed on DistributionId; the lister enumerates
// distributions via cloudfront:ListDistributions and feeds the
// DistributionId list into the standard CC GetResource fan-out.
// The interface is package-private so test fakes can satisfy it
// without depending on the full CloudFront client.
type cloudFrontDistributionsLister interface {
	ListDistributions(ctx context.Context, in *cloudfront.ListDistributionsInput, opts ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error)
}

// listCloudFrontDistributionIDs enumerates CloudFront distributions
// (global service) and returns the DistributionId for each. The CC
// primary identifier for AWS::CloudFront::MonitoringSubscription is
// DistributionId, and Terraform's import format for
// aws_cloudfront_monitoring_subscription is also the bare
// DistributionId — passthrough.
//
// Distributions without a monitoring subscription will surface
// ResourceNotFoundException on the downstream CC GetResource call;
// the discoverer's per-item soft-fail handles them via ServiceWarn
// without aborting the region scan.
//
// CloudFront ListDistributions paginates via `Marker` (string cursor;
// the next-page cursor field is `NextMarker` inside DistributionList).
// The terminator condition mirrors listCloudFrontFunctions: break on
// both nil AND empty-string cursors.
func listCloudFrontDistributionIDs(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := cloudfront.NewFromConfig(awsCfg, func(o *cloudfront.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listCloudFrontDistributionIDsWithClient(ctx, client)
}

func listCloudFrontDistributionIDsWithClient(ctx context.Context, client cloudFrontDistributionsLister) ([]string, error) {
	ids := []string{}
	var marker *string
	for {
		page, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("cloudfront:ListDistributions: %w", err)
		}
		if page.DistributionList != nil {
			for _, d := range page.DistributionList.Items {
				id := aws.ToString(d.Id)
				if id == "" {
					continue
				}
				ids = append(ids, id)
			}
		}
		next := ""
		if page.DistributionList != nil {
			next = aws.ToString(page.DistributionList.NextMarker)
		}
		if next == "" {
			break
		}
		marker = aws.String(next)
	}
	return ids, nil
}

// cloudWatchLogGroupsLister is the narrow subset of the CloudWatch
// Logs SDK used by the aws_cloudwatch_log_stream ParentLister
// enumerator (#14h). LogStream is parent-scoped on LogGroupName: CC
// ListResources without a ResourceModel returns
// InvalidRequestException ("Required property: [LogGroupName] not
// found"); the parent lister enumerates log groups via
// logs:DescribeLogGroups and wraps each name into a JSON
// ResourceModel for the fan-out. The interface is package-private so
// test fakes can satisfy it without depending on the full CWL
// client.
type cloudWatchLogGroupsLister interface {
	DescribeLogGroups(ctx context.Context, in *cloudwatchlogs.DescribeLogGroupsInput, opts ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
}

// listCloudWatchLogGroupsWithClient enumerates CloudWatch Logs log
// groups via the injected client and returns the LogGroupName for
// each. Used as the inner SDK call by
// listCloudWatchLogGroupsAsResourceModelsWithClient.
//
// DescribeLogGroups paginates via NextToken (string cursor). The
// terminator condition mirrors the other listers: break on both nil
// AND empty-string cursors. Log groups with empty / missing names are
// skipped defensively (the SDK contract permits the field to be nil).
func listCloudWatchLogGroupsWithClient(ctx context.Context, client cloudWatchLogGroupsLister) ([]string, error) {
	names := []string{}
	var nextToken *string
	for {
		page, err := client.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("logs:DescribeLogGroups: %w", err)
		}
		for _, lg := range page.LogGroups {
			n := aws.ToString(lg.LogGroupName)
			if n == "" {
				continue
			}
			names = append(names, n)
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return names, nil
}

// listCloudWatchLogGroupsAsResourceModels enumerates CloudWatch Logs
// log groups in the region and wraps each name into a JSON
// ResourceModel `{"LogGroupName":"…"}` for feeding into Cloud Control
// ListResources for AWS::Logs::LogStream (#14h). The parent-scoped
// LogStream type requires a non-empty ResourceModel — CC returns
// InvalidRequestException when LogGroupName is missing. Reuses
// listCloudWatchLogGroups for the underlying SDK call so pagination
// semantics are shared.
//
// Returns a non-nil empty slice on accounts with zero log groups so
// downstream consumers see "[]" not "null" through the JSON-marshal
// pipeline (#255 contract).
func listCloudWatchLogGroupsAsResourceModels(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := cloudwatchlogs.NewFromConfig(awsCfg, func(o *cloudwatchlogs.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listCloudWatchLogGroupsAsResourceModelsWithClient(ctx, client)
}

// listCloudWatchLogGroupsAsResourceModelsWithClient is the test seam
// for the wrap-shape contract. Production callers go through
// listCloudWatchLogGroupsAsResourceModels which constructs the real
// SDK client; tests drive the wrap logic via the cloudWatchLogGroupsLister
// interface against a fake.
func listCloudWatchLogGroupsAsResourceModelsWithClient(ctx context.Context, client cloudWatchLogGroupsLister) ([]string, error) {
	names, err := listCloudWatchLogGroupsWithClient(ctx, client)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(names))
	for _, n := range names {
		models = append(models, fmt.Sprintf(`{"LogGroupName":%q}`, n))
	}
	return models, nil
}

// =====================================================================
// Bundle 14i — IAM service-linked role SDKLister
// =====================================================================

// iamRolesLister is the narrow subset of the IAM SDK used by the
// aws_iam_service_linked_role SDKLister enumerator (#14i). It consumes
// iam:ListRoles output, filters to SLR roles (Path ==
// "/aws-service-role/<service>.amazonaws.com/...") and emits one
// AWSServiceName per role.
//
// The interface is package-private so test fakes can satisfy it without
// depending on the full IAM client surface (already used by iamUsersLister
// + iamGroupsLister with a different method set; declaring a fresh
// interface keeps the per-call dependency surface minimal).
type iamRolesLister interface {
	ListRoles(ctx context.Context, in *iam.ListRolesInput, opts ...func(*iam.Options)) (*iam.ListRolesOutput, error)
}

// iamServiceRolePathPrefix is the path AWS stamps on every service-
// linked role at creation time. Service-linked roles ALWAYS live under
// "/aws-service-role/<service-hostname>/<...>" — both the IAM console
// and CLI reject creates that don't match this shape, so the prefix
// check is a sound discriminator without false positives.
const iamServiceRolePathPrefix = "/aws-service-role/"

// listIAMServiceLinkedRoleServiceNames enumerates IAM roles (global)
// and emits the canonical AWSServiceName for each service-linked role.
// Used as the SDKLister for AWS::IAM::ServiceLinkedRole (#14i) — CC
// ListResources returns UnsupportedActionException for the type
// because SLRs are AWS-managed and have no LIST handler.
//
// AWSServiceName is the canonical service principal hostname (e.g.
// "elasticache.amazonaws.com"). For a service-linked role with Path
// "/aws-service-role/elasticache.amazonaws.com/" the service name is
// the SECOND path segment. We extract it deterministically by
// trimming the well-known SLR path prefix and taking everything up to
// the next "/".
//
// CC GetResource for AWS::IAM::ServiceLinkedRole is keyed on
// AWSServiceName (verified against the CFN schema's primaryIdentifier
// of [/properties/AWSServiceName]). Two SLRs for the same service is
// impossible by construction — IAM rejects duplicate-service SLR
// creates — so the AWSServiceName-as-identifier is unique per role.
//
// Returns a non-nil empty slice on accounts with zero SLRs (#255).
func listIAMServiceLinkedRoleServiceNames(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := iam.NewFromConfig(awsCfg, func(o *iam.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listIAMServiceLinkedRoleServiceNamesWithClient(ctx, client)
}

func listIAMServiceLinkedRoleServiceNamesWithClient(ctx context.Context, client iamRolesLister) ([]string, error) {
	names := []string{}
	seen := map[string]bool{}
	var marker *string
	for {
		page, err := client.ListRoles(ctx, &iam.ListRolesInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("iam:ListRoles: %w", err)
		}
		for _, r := range page.Roles {
			path := aws.ToString(r.Path)
			if !strings.HasPrefix(path, iamServiceRolePathPrefix) {
				continue
			}
			// Path is "/aws-service-role/<service>.amazonaws.com/..." —
			// trim the prefix, then take everything up to the next "/".
			rest := strings.TrimPrefix(path, iamServiceRolePathPrefix)
			service := rest
			if idx := strings.Index(rest, "/"); idx >= 0 {
				service = rest[:idx]
			}
			if service == "" {
				continue
			}
			// One SLR per service-principal by IAM construction;
			// dedup defensively in case a malformed account state
			// (e.g. stale ListRoles cursor) surfaces a duplicate.
			if seen[service] {
				continue
			}
			seen[service] = true
			names = append(names, service)
		}
		if !page.IsTruncated || page.Marker == nil || aws.ToString(page.Marker) == "" {
			break
		}
		marker = page.Marker
	}
	return names, nil
}

// =====================================================================
// Phase A.2 — IAM RolePolicy SDKLister (#466)
// =====================================================================

// iamRolePoliciesLister is the narrow subset of the IAM SDK used by the
// aws_iam_role_policy SDKLister. ListRoles enumerates the outer set;
// ListRolePolicies enumerates the inner set (inline policies per role).
// We keep the interface narrow on purpose so test fakes do not need to
// implement the entire IAM client surface.
type iamRolePoliciesLister interface {
	ListRoles(ctx context.Context, in *iam.ListRolesInput, opts ...func(*iam.Options)) (*iam.ListRolesOutput, error)
	ListRolePolicies(ctx context.Context, in *iam.ListRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error)
}

// listIAMRolePolicyIdentifiers enumerates IAM inline role policies
// (global) and emits the CC compound primary identifier for each. Used
// as the SDKLister for AWS::IAM::RolePolicy (#466) — CC ListResources
// returns UnsupportedActionException for the type because inline
// policies live under a parent role rather than as top-level IAM
// resources.
//
// AWS::IAM::RolePolicy's CC primaryIdentifier is
// [/properties/PolicyName, /properties/RoleName] (verified against the
// public CFN schema:
//
//	https://schema.cloudformation.us-east-1.amazonaws.com/aws-iam-rolepolicy.json
//
// `primaryIdentifier: [/properties/PolicyName, /properties/RoleName]`).
// The framework joins compound identifiers with `|` in the order
// declared by the schema, so emitted identifiers are
// "<PolicyName>|<RoleName>". The TF import rewrite (in
// cloudcontrol_types.go) swaps the parts and joins them with `:` —
// terraform-provider-aws v6.x docs:
// "<role_name>:<role_policy_name>".
//
// We walk iam:ListRoles paginated → for each role, iam:ListRolePolicies
// paginated. A single role can have many inline policies; the outer
// loop is the parent enumerator and the inner loop is the per-role
// fan-out. Service-linked roles (Path == "/aws-service-role/...") are
// filtered out because they cannot hold inline policies (IAM rejects
// PutRolePolicy on them), so issuing ListRolePolicies for them would
// burn API budget on a guaranteed-empty result.
//
// Returns a non-nil empty slice on accounts with zero inline role
// policies (#255 contract).
func listIAMRolePolicyIdentifiers(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := iam.NewFromConfig(awsCfg, func(o *iam.Options) {
		if region != "" {
			o.Region = region
		}
	})
	return listIAMRolePolicyIdentifiersWithClient(ctx, client)
}

func listIAMRolePolicyIdentifiersWithClient(ctx context.Context, client iamRolePoliciesLister) ([]string, error) {
	ids := []string{}
	seen := map[string]bool{}

	// Outer loop: enumerate all IAM roles (paginated).
	var roleMarker *string
	for {
		rolesPage, err := client.ListRoles(ctx, &iam.ListRolesInput{Marker: roleMarker})
		if err != nil {
			return nil, fmt.Errorf("iam:ListRoles: %w", err)
		}
		for _, r := range rolesPage.Roles {
			roleName := aws.ToString(r.RoleName)
			if roleName == "" {
				continue
			}
			// Skip service-linked roles — they cannot hold inline policies
			// (IAM rejects PutRolePolicy on /aws-service-role/ roles), so
			// the per-role ListRolePolicies would always return zero.
			if strings.HasPrefix(aws.ToString(r.Path), iamServiceRolePathPrefix) {
				continue
			}

			// Inner loop: enumerate inline policies for this role.
			var polMarker *string
			for {
				polPage, err := client.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{
					RoleName: aws.String(roleName),
					Marker:   polMarker,
				})
				if err != nil {
					return nil, fmt.Errorf("iam:ListRolePolicies(role=%q): %w", roleName, err)
				}
				for _, policyName := range polPage.PolicyNames {
					if policyName == "" {
						continue
					}
					// CC compound identifier order matches the schema's
					// primaryIdentifier declaration: PolicyName first.
					id := policyName + "|" + roleName
					if seen[id] {
						continue
					}
					seen[id] = true
					ids = append(ids, id)
				}
				if !polPage.IsTruncated || polPage.Marker == nil || aws.ToString(polPage.Marker) == "" {
					break
				}
				polMarker = polPage.Marker
			}
		}
		if !rolesPage.IsTruncated || rolesPage.Marker == nil || aws.ToString(rolesPage.Marker) == "" {
			break
		}
		roleMarker = rolesPage.Marker
	}
	return ids, nil
}
