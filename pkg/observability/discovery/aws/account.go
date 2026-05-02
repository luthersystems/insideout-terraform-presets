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

func inspectAccount(ctx context.Context, cfg aws.Config, action, _ string) (any, error) {
	switch action {
	case "get-info":
		stsClient := sts.NewFromConfig(cfg)
		iamClient := iam.NewFromConfig(cfg)
		info := make(map[string]any)

		identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err == nil {
			info["AccountId"] = aws.ToString(identity.Account)
			info["Arn"] = aws.ToString(identity.Arn)
			info["UserId"] = aws.ToString(identity.UserId)
		} else {
			log.Printf("[aws-inspect] account info: sts.GetCallerIdentity failed: %v", err)
		}

		info["Region"] = cfg.Region

		aliases, err := iamClient.ListAccountAliases(ctx, &iam.ListAccountAliasesInput{})
		if err == nil && len(aliases.AccountAliases) > 0 {
			info["AccountAliases"] = aliases.AccountAliases
		}

		summary, err := iamClient.GetAccountSummary(ctx, &iam.GetAccountSummaryInput{})
		if err == nil {
			info["AccountSummary"] = summary.SummaryMap
		}

		return info, nil

	default:
		return nil, unsupportedActionError("account", action)
	}
}
