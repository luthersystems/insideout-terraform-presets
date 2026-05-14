package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
)

// s3SubresourceClient is the narrow subset of the AWS S3 API the SDK-only
// sub-resource discoverers issue. Both real *s3.Client and in-test fakes
// satisfy this interface; production code constructs the real client via
// s3.NewFromConfig from each FetchItem closure.
//
// Only the bucket-enumeration and 5 sub-resource Get* RPCs are listed —
// the discoverers do not mutate state.
type s3SubresourceClient interface {
	ListBuckets(ctx context.Context, in *s3.ListBucketsInput, opts ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketVersioning(ctx context.Context, in *s3.GetBucketVersioningInput, opts ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
	GetBucketLifecycleConfiguration(ctx context.Context, in *s3.GetBucketLifecycleConfigurationInput, opts ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error)
	GetBucketOwnershipControls(ctx context.Context, in *s3.GetBucketOwnershipControlsInput, opts ...func(*s3.Options)) (*s3.GetBucketOwnershipControlsOutput, error)
	GetPublicAccessBlock(ctx context.Context, in *s3.GetPublicAccessBlockInput, opts ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error)
	GetBucketEncryption(ctx context.Context, in *s3.GetBucketEncryptionInput, opts ...func(*s3.Options)) (*s3.GetBucketEncryptionOutput, error)
}

// newS3SubresourceClient is the production factory injected into each
// FetchItem closure. Tests override the closure stored on
// sdkOnlySubresourceDiscoverer indirectly via a package-level swap of
// this factory — see sdkonly_s3_test.go.
//
// Holding a package-level var (rather than threading a factory through
// every FetchItem signature) keeps the cloudControlConfig-symmetric
// shape of sdkOnlySubresourceConfig and avoids closure-stuffing in every
// per-type FetchItem registration. The production code path is purely
// functional; the var is only swapped in test main / setup helpers.
var newS3SubresourceClient = func(awsCfg aws.Config, region string) s3SubresourceClient {
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// listS3Buckets enumerates every S3 bucket in the account (S3 has no
// per-region list — ListBuckets returns the account-global set; the
// BucketRegion filter restricts the result to buckets whose region
// matches `region`). Used as the ListParents callback for all 5 S3
// sub-resource configs.
//
// Pagination: ListBuckets v2 introduced ContinuationToken; the loop
// drains every page so a 1000+ bucket account doesn't silently truncate.
func listS3Buckets(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := newS3SubresourceClient(awsCfg, region)
	return listS3BucketsWithClient(ctx, client, region)
}

func listS3BucketsWithClient(ctx context.Context, client s3SubresourceClient, region string) ([]string, error) {
	names := []string{}
	var token *string
	for {
		in := &s3.ListBucketsInput{ContinuationToken: token}
		if region != "" {
			in.BucketRegion = aws.String(region)
		}
		out, err := client.ListBuckets(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("s3:ListBuckets (region=%s): %w", region, err)
		}
		for _, b := range out.Buckets {
			name := aws.ToString(b.Name)
			if name == "" {
				continue
			}
			names = append(names, name)
		}
		if out.ContinuationToken == nil || aws.ToString(out.ContinuationToken) == "" {
			break
		}
		token = out.ContinuationToken
	}
	return names, nil
}

// isS3NotSetError returns true when err is the service-native "this
// sub-resource is not configured on the bucket" signal for the given
// error code. AWS S3 surfaces these as either typed smithy errors or
// generic APIError shapes depending on SDK version; we check both.
//
// Known codes (per AWS S3 REST API reference, verified against
// aws-sdk-go-v2/service/s3 v1.100.x):
//
//   - NoSuchLifecycleConfiguration (GetBucketLifecycleConfiguration)
//   - OwnershipControlsNotFoundError (GetBucketOwnershipControls)
//   - NoSuchPublicAccessBlockConfiguration (GetPublicAccessBlock)
//   - ServerSideEncryptionConfigurationNotFoundError (GetBucketEncryption)
//
// GetBucketVersioning intentionally has no NotFound code: when
// versioning has never been enabled the SDK returns success with
// Status="" and MFADelete="", which the per-type FetchItem closure
// interprets as "sub-resource does not exist."
func isS3NotSetError(err error, codes ...string) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		got := apiErr.ErrorCode()
		for _, want := range codes {
			if got == want {
				return true
			}
		}
	}
	return false
}

// sdkOnlySubresourceTypeConfigs is the registry of Terraform resource
// types routed through the SDK-only sub-resource discoverer (Bundle
// 14k1). Each entry maps one TFType to a parent CloudFormation type
// plus the per-sub-resource FetchItem implementation. The list is
// iterated at aggregator construction time
// (NewAWSDiscovererWithConcurrency in awsdiscover.go) to populate
// byType in one shot — symmetric with cloudControlTypeConfigs.
//
// Adding a new type means: (1) confirm the parent's RGT/CC discoverer
// already exists, (2) implement a FetchItem closure that distinguishes
// "configured" vs "not configured" via service-native NotFound codes,
// (3) append the config below, (4) extend
// pkg/insideout-import/registry/registry.go::awsTypes, (5) extend
// pkg/composer/imported/category.go::categoryByTFType, (6) extend
// pkg/insideout-import/permissions/aws.json with per-RPC permissions,
// (7) for untaggable sub-resources confirm the parent's
// tests/lint-project-tag.sh::NON_TAGGABLE_AWS allowlist covers the type.
var sdkOnlySubresourceTypeConfigs = []sdkOnlySubresourceConfig{
	// =====================================================================
	// S3 bucket sub-resources (Bundle 14k1 — issue #452)
	// =====================================================================
	//
	// All 5 share:
	//   - Parent: AWS::S3::Bucket (registered in cloudControlTypeConfigs).
	//   - ListParents: listS3Buckets (account-global, region-filtered).
	//   - SkipProjectTagFilter: true (untaggable).
	//   - ImportID: bucket name (Terraform's import format for all 5 is
	//     just the bucket name — confirmed against terraform-provider-aws
	//     v6.x source for each resource's Importer).
	//   - NameHint: "<bucket>-<sub-resource-slug>" so address generation
	//     can distinguish a bucket's versioning from its lifecycle config.
	//   - Tags: empty map (untaggable).

	{
		TFType:               "aws_s3_bucket_versioning",
		Slug:                 "s3_bucket_versioning",
		ParentCFNType:        "AWS::S3::Bucket",
		SkipProjectTagFilter: true,
		ListParents:          listS3Buckets,
		FetchItem:            fetchS3BucketVersioning,
		ImportIDFromParent:   func(parentID string, _ map[string]any) string { return parentID },
		NameHintFromParent:   func(parentID string, _ map[string]any) string { return parentID + "-versioning" },
	},
	{
		TFType:               "aws_s3_bucket_lifecycle_configuration",
		Slug:                 "s3_bucket_lifecycle_configuration",
		ParentCFNType:        "AWS::S3::Bucket",
		SkipProjectTagFilter: true,
		ListParents:          listS3Buckets,
		FetchItem:            fetchS3BucketLifecycleConfiguration,
		ImportIDFromParent:   func(parentID string, _ map[string]any) string { return parentID },
		NameHintFromParent:   func(parentID string, _ map[string]any) string { return parentID + "-lifecycle" },
	},
	{
		TFType:               "aws_s3_bucket_ownership_controls",
		Slug:                 "s3_bucket_ownership_controls",
		ParentCFNType:        "AWS::S3::Bucket",
		SkipProjectTagFilter: true,
		ListParents:          listS3Buckets,
		FetchItem:            fetchS3BucketOwnershipControls,
		ImportIDFromParent:   func(parentID string, _ map[string]any) string { return parentID },
		NameHintFromParent:   func(parentID string, _ map[string]any) string { return parentID + "-ownership" },
	},
	{
		TFType:               "aws_s3_bucket_public_access_block",
		Slug:                 "s3_bucket_public_access_block",
		ParentCFNType:        "AWS::S3::Bucket",
		SkipProjectTagFilter: true,
		ListParents:          listS3Buckets,
		FetchItem:            fetchS3BucketPublicAccessBlock,
		ImportIDFromParent:   func(parentID string, _ map[string]any) string { return parentID },
		NameHintFromParent:   func(parentID string, _ map[string]any) string { return parentID + "-public-access-block" },
	},
	{
		TFType:               "aws_s3_bucket_server_side_encryption_configuration",
		Slug:                 "s3_bucket_server_side_encryption_configuration",
		ParentCFNType:        "AWS::S3::Bucket",
		SkipProjectTagFilter: true,
		ListParents:          listS3Buckets,
		FetchItem:            fetchS3BucketServerSideEncryption,
		ImportIDFromParent:   func(parentID string, _ map[string]any) string { return parentID },
		NameHintFromParent:   func(parentID string, _ map[string]any) string { return parentID + "-sse" },
	},

	// =====================================================================
	// Bundle 14k2 — final 5 deferred AWS types via SDK-only pattern
	// (issue #456). Four of the five emit N items per parent and use the
	// 14k2-introduced FetchItems plural variant; one (DDB contributor
	// insights) still uses the single-emission FetchItem because the
	// underlying resource is one-per-table.
	//
	// `aws_security_group_rule` is intentionally NOT registered here.
	// Terraform's legacy `aws_security_group_rule` uses a synthetic
	// hash-based ID (`sgrule-<hash>`) computed from rule attributes that
	// cannot be reliably reconstructed by the discoverer; emitting an
	// import ID we cannot prove is correct would cause silent terraform
	// import failures downstream. The replacement resources
	// `aws_vpc_security_group_ingress_rule` /
	// `aws_vpc_security_group_egress_rule` (which carry real-ARN-shape
	// security_group_rule_id values returned by EC2) are the supported
	// path forward for in-preset SG-rule discovery — tracked as a
	// follow-up to #456.
	// =====================================================================

	{
		// `aws_dynamodb_contributor_insights` — one per DDB table, exists
		// iff ContributorInsightsStatus is ENABLED or ENABLING. Untaggable
		// (the TF resource is a meta-binding on the table) so
		// SkipProjectTagFilter=true and parent enumeration falls back to
		// dynamodb:ListTables when the RGT cache for AWS::DynamoDB::Table
		// is empty / cold. Import ID is the bare table name.
		TFType:               "aws_dynamodb_contributor_insights",
		Slug:                 "dynamodb_contributor_insights",
		ParentCFNType:        "AWS::DynamoDB::Table",
		SkipProjectTagFilter: true,
		ListParents:          listDDBTables,
		FetchItem:            fetchDDBContributorInsights,
		ImportIDFromParent:   func(parentID string, _ map[string]any) string { return parentID },
		NameHintFromParent:   func(parentID string, _ map[string]any) string { return parentID + "-contributor-insights" },
	},
	{
		// `aws_iam_role_policy_attachment` — multi-emit. One IAM role
		// yields one TF resource per attached managed policy. Import
		// format is "<role_name>/<policy_arn>". IAM is a global service;
		// IsGlobal=true ensures the discoverer issues one pass with
		// region="" rather than fanning out per regional endpoint.
		// Service-linked roles are filtered out at ListParents time
		// (they cannot attach managed policies; see
		// listIAMRoleNamesNonSLR).
		TFType:               "aws_iam_role_policy_attachment",
		Slug:                 "iam_role_policy_attachment",
		ParentCFNType:        "AWS::IAM::Role",
		IsGlobal:             true,
		SkipProjectTagFilter: true,
		ListParents:          listIAMRoleNamesNonSLR,
		FetchItems:           fetchIAMRolePolicyAttachments,
	},
	{
		// `aws_wafv2_web_acl_association` — multi-emit. One WAFv2 Web
		// ACL yields one TF resource per associated resource ARN
		// (ALB / API Gateway / AppSync / Cognito user pool / App Runner
		// / Verified Access / Amplify). Import format is
		// "<resource_arn>,<web_acl_arn>". ListParents enumerates both
		// REGIONAL ACLs (in any region) and CLOUDFRONT-scoped ACLs (only
		// in us-east-1 per the WAFv2 API docs); the framework loops the
		// configured args.Regions and the per-region call only surfaces
		// CLOUDFRONT when region=us-east-1.
		TFType:               "aws_wafv2_web_acl_association",
		Slug:                 "wafv2_web_acl_association",
		ParentCFNType:        "AWS::WAFv2::WebACL",
		SkipProjectTagFilter: true,
		ListParents:          listWAFv2WebACLs,
		FetchItems:           fetchWAFv2WebACLAssociations,
	},
	{
		// `aws_autoscaling_group_tag` — multi-emit. One Auto Scaling
		// Group yields one TF resource per (asg, tag_key) pair. Tags
		// come back inline on DescribeAutoScalingGroups; no additional
		// SDK call needed. Import format is "<asg_name>,<tag_key>".
		// The TF resource exists alongside the inline `tag` blocks on
		// `aws_autoscaling_group` for incremental tag management;
		// presets that declare every tag inline will surface duplicate
		// state for those tags here, which is the expected import-side
		// behavior (the operator can choose which side owns each tag).
		TFType:               "aws_autoscaling_group_tag",
		Slug:                 "autoscaling_group_tag",
		ParentCFNType:        "AWS::AutoScaling::AutoScalingGroup",
		SkipProjectTagFilter: true,
		ListParents:          listASGNames,
		FetchItems:           fetchASGTags,
	},
}

// fetchS3BucketVersioning implements FetchItem for aws_s3_bucket_versioning.
//
// "exists" semantics: the TF resource exists iff the bucket has had
// versioning configured (Suspended or Enabled) OR MFA Delete configured.
// A bucket that has never had versioning configured returns Status="" and
// MFADelete="" — those map to exists=false. Unlike the other 4 sub-
// resources, GetBucketVersioning has no NoSuch* error code: AWS treats
// "versioning never set" as a successful empty response.
func fetchS3BucketVersioning(ctx context.Context, awsCfg aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
	return fetchS3BucketVersioningWithClient(ctx, newS3SubresourceClient(awsCfg, region), parentID)
}

func fetchS3BucketVersioningWithClient(ctx context.Context, client s3SubresourceClient, parentID string) (bool, map[string]any, map[string]string, error) {
	out, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(parentID)})
	if err != nil {
		// NoSuchBucket means the parent vanished between list and read —
		// surface as exists=false rather than warn-spamming.
		if isS3NotSetError(err, "NoSuchBucket") {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}
	if out == nil || (out.Status == "" && out.MFADelete == "") {
		return false, nil, nil, nil
	}
	props := map[string]any{
		"Bucket":    parentID,
		"Status":    string(out.Status),
		"MFADelete": string(out.MFADelete),
	}
	return true, props, s3SubresourceNativeIDs(parentID), nil
}

// fetchS3BucketLifecycleConfiguration implements FetchItem for
// aws_s3_bucket_lifecycle_configuration.
//
// "exists" semantics: TF resource exists iff Rules is non-empty.
// NoSuchLifecycleConfiguration is the service-native "not set" signal —
// AWS returns this as the API error code (not a typed Go error).
func fetchS3BucketLifecycleConfiguration(ctx context.Context, awsCfg aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
	return fetchS3BucketLifecycleConfigurationWithClient(ctx, newS3SubresourceClient(awsCfg, region), parentID)
}

func fetchS3BucketLifecycleConfigurationWithClient(ctx context.Context, client s3SubresourceClient, parentID string) (bool, map[string]any, map[string]string, error) {
	out, err := client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(parentID)})
	if err != nil {
		if isS3NotSetError(err, "NoSuchLifecycleConfiguration", "NoSuchBucket") {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}
	if out == nil || len(out.Rules) == 0 {
		return false, nil, nil, nil
	}
	props := map[string]any{
		"Bucket":    parentID,
		"RuleCount": len(out.Rules),
	}
	return true, props, s3SubresourceNativeIDs(parentID), nil
}

// fetchS3BucketOwnershipControls implements FetchItem for
// aws_s3_bucket_ownership_controls.
//
// "exists" semantics: TF resource exists iff OwnershipControls.Rules is
// non-empty. OwnershipControlsNotFoundError is the service-native "not
// set" signal.
//
// Note: as of 2023 AWS automatically applies BucketOwnerEnforced to new
// buckets, so most buckets will have OwnershipControls set. The TF
// resource still imports cleanly for those.
func fetchS3BucketOwnershipControls(ctx context.Context, awsCfg aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
	return fetchS3BucketOwnershipControlsWithClient(ctx, newS3SubresourceClient(awsCfg, region), parentID)
}

func fetchS3BucketOwnershipControlsWithClient(ctx context.Context, client s3SubresourceClient, parentID string) (bool, map[string]any, map[string]string, error) {
	out, err := client.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{Bucket: aws.String(parentID)})
	if err != nil {
		if isS3NotSetError(err, "OwnershipControlsNotFoundError", "NoSuchOwnershipControls", "NoSuchBucket") {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}
	if out == nil || out.OwnershipControls == nil || len(out.OwnershipControls.Rules) == 0 {
		return false, nil, nil, nil
	}
	props := map[string]any{
		"Bucket":             parentID,
		"OwnershipRuleCount": len(out.OwnershipControls.Rules),
	}
	return true, props, s3SubresourceNativeIDs(parentID), nil
}

// fetchS3BucketPublicAccessBlock implements FetchItem for
// aws_s3_bucket_public_access_block.
//
// "exists" semantics: TF resource exists iff the response is returned
// with a non-nil PublicAccessBlockConfiguration. AWS S3 surfaces "not
// set" via NoSuchPublicAccessBlockConfiguration.
func fetchS3BucketPublicAccessBlock(ctx context.Context, awsCfg aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
	return fetchS3BucketPublicAccessBlockWithClient(ctx, newS3SubresourceClient(awsCfg, region), parentID)
}

func fetchS3BucketPublicAccessBlockWithClient(ctx context.Context, client s3SubresourceClient, parentID string) (bool, map[string]any, map[string]string, error) {
	out, err := client.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: aws.String(parentID)})
	if err != nil {
		if isS3NotSetError(err, "NoSuchPublicAccessBlockConfiguration", "NoSuchBucket") {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}
	if out == nil || out.PublicAccessBlockConfiguration == nil {
		return false, nil, nil, nil
	}
	props := map[string]any{
		"Bucket": parentID,
	}
	return true, props, s3SubresourceNativeIDs(parentID), nil
}

// fetchS3BucketServerSideEncryption implements FetchItem for
// aws_s3_bucket_server_side_encryption_configuration.
//
// "exists" semantics: TF resource exists iff
// ServerSideEncryptionConfiguration.Rules is non-empty.
// ServerSideEncryptionConfigurationNotFoundError is the service-native
// "not set" signal.
//
// Note: as of January 2023 AWS applies SSE-S3 (AES256) by default to
// all new buckets, so most buckets will surface here even if the TF
// preset never declared an explicit encryption config.
func fetchS3BucketServerSideEncryption(ctx context.Context, awsCfg aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
	return fetchS3BucketServerSideEncryptionWithClient(ctx, newS3SubresourceClient(awsCfg, region), parentID)
}

func fetchS3BucketServerSideEncryptionWithClient(ctx context.Context, client s3SubresourceClient, parentID string) (bool, map[string]any, map[string]string, error) {
	out, err := client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(parentID)})
	if err != nil {
		if isS3NotSetError(err, "ServerSideEncryptionConfigurationNotFoundError", "NoSuchEncryptionConfiguration", "NoSuchBucket") {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}
	if out == nil || out.ServerSideEncryptionConfiguration == nil || len(out.ServerSideEncryptionConfiguration.Rules) == 0 {
		return false, nil, nil, nil
	}
	props := map[string]any{
		"Bucket":    parentID,
		"RuleCount": len(out.ServerSideEncryptionConfiguration.Rules),
	}
	return true, props, s3SubresourceNativeIDs(parentID), nil
}

// s3SubresourceNativeIDs builds the NativeIDs map shared across all 5
// S3 sub-resource types. "bucket" is the load-bearing key — it's what
// the Terraform AWS provider's import format expects (the bucket name)
// and what reliable's UI panels surface for cross-resource navigation.
func s3SubresourceNativeIDs(bucket string) map[string]string {
	return map[string]string{"bucket": bucket}
}

// Compile-time sanity: every config in sdkOnlySubresourceTypeConfigs
// must satisfy the minimal contract its discoverer asserts at runtime.
// FetchItem (single-emission, Bundle 14k1) and FetchItems
// (multi-emission, Bundle 14k2) are mutually exclusive — exactly one
// must be set. When FetchItem is set, ImportIDFromParent and
// NameHintFromParent are also required (the discoverer derives the
// import ID from the parent identifier). When FetchItems is set, those
// closures are ignored because each emission carries its own
// addressing fields. Surfaces a registration regression at test-time
// rather than during a live discover run.
var _ = func() bool {
	for _, cfg := range sdkOnlySubresourceTypeConfigs {
		if cfg.TFType == "" || cfg.Slug == "" {
			panic("sdkOnlySubresourceTypeConfigs entry missing TFType or Slug")
		}
		if cfg.ListParents == nil {
			panic("sdkOnlySubresourceTypeConfigs entry missing ListParents: " + cfg.TFType)
		}
		if cfg.FetchItem == nil && cfg.FetchItems == nil {
			panic("sdkOnlySubresourceTypeConfigs entry missing FetchItem or FetchItems: " + cfg.TFType)
		}
		if cfg.FetchItem != nil && cfg.FetchItems != nil {
			panic("sdkOnlySubresourceTypeConfigs entry sets both FetchItem and FetchItems (mutually exclusive): " + cfg.TFType)
		}
		if cfg.FetchItem != nil && (cfg.ImportIDFromParent == nil || cfg.NameHintFromParent == nil) {
			panic("sdkOnlySubresourceTypeConfigs entry missing ImportIDFromParent or NameHintFromParent (required when FetchItem is set): " + cfg.TFType)
		}
		if !strings.HasPrefix(cfg.TFType, "aws_") {
			panic("sdkOnlySubresourceTypeConfigs entry TFType must start with aws_: " + cfg.TFType)
		}
	}
	return true
}()

// Compile-time pin: keep the types package referenced even when only
// the smithy-error path consumes its constants. Without this anchor a
// future refactor that drops the typed-error fast path would silently
// elide the import.
var _ s3types.LifecycleRule
