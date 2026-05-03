// Account-summary inspector.
//
// Ported from reliable internal/agentapi/aws_inspect.go (account:
// 1774-1812). Issue #225: AWSServiceActions advertised "account" but the
// discovery dispatcher had no arm — calls fell through to
// ErrUnsupportedService.
//
// The issue text mentions aws-sdk-go-v2/service/account; reliable's real
// implementation composes sts.GetCallerIdentity + iam.ListAccountAliases +
// iam.GetAccountSummary. Both SDKs are already in go.mod, and this is the
// shape reliable's frontend reads, so the port preserves it.
//
// Per-call errors log+continue rather than fail — partial answers
// (AccountId without Aliases, etc.) are more useful in the panel than a
// 500. Mirrors reliable's behaviour.

package aws

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// iamAccountInfoClient is the IAM half of the get-info compose. The
// stsAccountClient interface for GetCallerIdentity already exists in
// data.go and is reused here.
type iamAccountInfoClient interface {
	ListAccountAliases(ctx context.Context, params *iam.ListAccountAliasesInput, optFns ...func(*iam.Options)) (*iam.ListAccountAliasesOutput, error)
	GetAccountSummary(ctx context.Context, params *iam.GetAccountSummaryInput, optFns ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error)
}

func inspectAccount(ctx context.Context, cfg aws.Config, action, _ string) (any, error) {
	switch action {
	case "get-info":
		return gatherAccountInfo(ctx, cfg.Region, sts.NewFromConfig(cfg), iam.NewFromConfig(cfg)), nil
	default:
		return nil, unsupportedActionError("account", action)
	}
}

// gatherAccountInfo composes the three account-summary calls into a
// single map. Per-call errors log+continue so a partial answer
// (e.g. caller identity without aliases) still reaches the panel.
func gatherAccountInfo(ctx context.Context, region string, stsClient stsAccountClient, iamClient iamAccountInfoClient) map[string]any {
	info := make(map[string]any)

	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err == nil {
		info["AccountId"] = aws.ToString(identity.Account)
		info["Arn"] = aws.ToString(identity.Arn)
		info["UserId"] = aws.ToString(identity.UserId)
	} else {
		log.Printf("[aws-inspect] account info: sts.GetCallerIdentity failed: %v", err)
	}

	info["Region"] = region

	aliases, err := iamClient.ListAccountAliases(ctx, &iam.ListAccountAliasesInput{})
	if err == nil && len(aliases.AccountAliases) > 0 {
		info["AccountAliases"] = aliases.AccountAliases
	}

	summary, err := iamClient.GetAccountSummary(ctx, &iam.GetAccountSummaryInput{})
	if err == nil {
		info["AccountSummary"] = summary.SummaryMap
	}

	return info
}
