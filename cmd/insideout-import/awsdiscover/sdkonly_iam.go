package awsdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// iamRolePolicyAttachmentClient is the narrow subset of the IAM API
// the aws_iam_role_policy_attachment SDK-only sub-resource discoverer
// issues. Real *iam.Client and in-test fakes satisfy this interface;
// production code constructs the real client via iam.NewFromConfig
// from each FetchItems closure (factory at newIAMRPAClient).
type iamRolePolicyAttachmentClient interface {
	ListRoles(ctx context.Context, in *iam.ListRolesInput, opts ...func(*iam.Options)) (*iam.ListRolesOutput, error)
	ListAttachedRolePolicies(ctx context.Context, in *iam.ListAttachedRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
}

// newIAMRPAClient is the production factory injected into each IAM
// FetchItems / ListParents closure. Tests construct fakes directly
// and pass them to *WithClient helpers so every test runs under
// t.Parallel() without inter-test races.
//
// IAM is a global service; the region argument is ignored at the SDK
// level (IAM endpoint is always us-east-1 for non-FIPS), but kept in
// the factory signature for parity with the regional sub-resource
// clients.
var newIAMRPAClient = func(awsCfg aws.Config, _ string) iamRolePolicyAttachmentClient {
	return iam.NewFromConfig(awsCfg)
}

// listIAMRoleNamesNonSLR enumerates IAM role names suitable as
// parents for aws_iam_role_policy_attachment, dropping service-linked
// roles (Path starts with /aws-service-role/). Service-linked roles
// reject AttachRolePolicy with AccessDenied so listing them would
// produce N noisy per-parent ListAttachedRolePolicies failures on
// the downstream FetchItems fan-out.
//
// Mirrors the SLR-skip semantics of listIAMServiceLinkedRoleServiceNamesWithClient
// in cloudcontrol_listers.go but inverts the filter (non-SLR roles)
// and emits bare role names instead of service principals — the SDK-only
// sub-resource discoverer fans out by parent identifier directly.
//
// Returns a non-nil empty slice on accounts with zero roles (#255).
func listIAMRoleNamesNonSLR(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := newIAMRPAClient(awsCfg, region)
	return listIAMRoleNamesNonSLRWithClient(ctx, client)
}

func listIAMRoleNamesNonSLRWithClient(ctx context.Context, client iamRolePolicyAttachmentClient) ([]string, error) {
	names := []string{}
	var marker *string
	for {
		page, err := client.ListRoles(ctx, &iam.ListRolesInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("iam:ListRoles: %w", err)
		}
		for _, r := range page.Roles {
			name := aws.ToString(r.RoleName)
			if name == "" {
				continue
			}
			if strings.HasPrefix(aws.ToString(r.Path), iamServiceRolePathPrefix) {
				continue
			}
			names = append(names, name)
		}
		if !page.IsTruncated || page.Marker == nil || aws.ToString(page.Marker) == "" {
			break
		}
		marker = page.Marker
	}
	return names, nil
}

// fetchIAMRolePolicyAttachments implements FetchItems for
// aws_iam_role_policy_attachment.
//
// One parent (role) yields N emissions — one per attached managed
// policy. Terraform's import format for aws_iam_role_policy_attachment
// is "<role_name>/<policy_arn>" — verified against
// terraform-provider-aws v6.x internal/service/iam/role_policy_attachment.go::
// resourceRolePolicyAttachmentImport, which splits on "/" with N=2.
//
// NameHint = the policy ARN (the discriminating attribute per
// attachment; address generation uses it to disambiguate addresses
// when one role has many attachments).
//
// NativeIDs carry both the role and policy ARN so reliable's UI
// surfaces and dep-chase cross-resource navigation can follow the
// edge to either side without re-parsing the compound import ID.
//
// Pagination: iam:ListAttachedRolePolicies pages via Marker; the
// loop drains all pages so a 100+-attachment role doesn't silently
// truncate (IAM caps attachments per role at 20 by default, raised
// to 50 with quota requests, but the loop handles any count).
//
// NoSuchEntity on a role that disappeared between ListRoles and
// ListAttachedRolePolicies yields zero emissions (silently skipped)
// — the per-parent soft-fail in the framework converts other errors
// to ServiceWarn.
func fetchIAMRolePolicyAttachments(ctx context.Context, awsCfg aws.Config, region, parentID string) ([]subresourceEmission, error) {
	return fetchIAMRolePolicyAttachmentsWithClient(ctx, newIAMRPAClient(awsCfg, region), parentID)
}

func fetchIAMRolePolicyAttachmentsWithClient(ctx context.Context, client iamRolePolicyAttachmentClient, roleName string) ([]subresourceEmission, error) {
	emissions := []subresourceEmission{}
	var marker *string
	for {
		page, err := client.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
			RoleName: aws.String(roleName),
			Marker:   marker,
		})
		if err != nil {
			// NoSuchEntity = role vanished between ListRoles and
			// this fetch. Silently skip rather than warn-spam.
			if isAPIErrorCode(err, "NoSuchEntity", "NoSuchEntityException") {
				return emissions, nil
			}
			return nil, fmt.Errorf("iam:ListAttachedRolePolicies role=%q: %w", roleName, err)
		}
		for _, ap := range page.AttachedPolicies {
			policyARN := aws.ToString(ap.PolicyArn)
			if policyARN == "" {
				continue
			}
			emissions = append(emissions, subresourceEmission{
				ImportID: roleName + "/" + policyARN,
				NameHint: policyARN,
				NativeIDs: map[string]string{
					"role":       roleName,
					"policy_arn": policyARN,
				},
				Props: map[string]any{
					"RoleName":   roleName,
					"PolicyName": aws.ToString(ap.PolicyName),
					"PolicyArn":  policyARN,
				},
			})
		}
		if !page.IsTruncated || page.Marker == nil || aws.ToString(page.Marker) == "" {
			break
		}
		marker = page.Marker
	}
	return emissions, nil
}
