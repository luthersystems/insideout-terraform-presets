// Account-summary inspector tests. Covers the partial-failure
// log+continue contract: each of the three SDK calls (sts.GetCallerIdentity,
// iam.ListAccountAliases, iam.GetAccountSummary) can fail independently
// and gatherAccountInfo must still return whatever it managed to fetch.
//
// Ported from the InsideOut backend internal/agentapi/aws_inspect_test.go cases
// covering inspectAccount.

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIAMAccountClient implements iamAccountInfoClient for the
// account-info compose. Distinct from the bedrock_test.go fakeIAMClient
// (which only stubs GetRole) so the two test surfaces don't fight over
// the same struct shape.
type fakeIAMAccountClient struct {
	aliasesOut *iam.ListAccountAliasesOutput
	aliasesErr error
	summaryOut *iam.GetAccountSummaryOutput
	summaryErr error
}

func (f *fakeIAMAccountClient) ListAccountAliases(_ context.Context, _ *iam.ListAccountAliasesInput, _ ...func(*iam.Options)) (*iam.ListAccountAliasesOutput, error) {
	if f.aliasesErr != nil {
		return nil, f.aliasesErr
	}
	if f.aliasesOut == nil {
		return &iam.ListAccountAliasesOutput{}, nil
	}
	return f.aliasesOut, nil
}

func (f *fakeIAMAccountClient) GetAccountSummary(_ context.Context, _ *iam.GetAccountSummaryInput, _ ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error) {
	if f.summaryErr != nil {
		return nil, f.summaryErr
	}
	if f.summaryOut == nil {
		return &iam.GetAccountSummaryOutput{}, nil
	}
	return f.summaryOut, nil
}

func TestGatherAccountInfo_HappyPath(t *testing.T) {
	t.Parallel()
	stsClient := &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{
			Account: aws.String("123456789012"),
			Arn:     aws.String("arn:aws:iam::123456789012:user/alice"),
			UserId:  aws.String("AIDA1234567EXAMPLE"),
		},
	}
	iamClient := &fakeIAMAccountClient{
		aliasesOut: &iam.ListAccountAliasesOutput{
			AccountAliases: []string{"my-org"},
		},
		summaryOut: &iam.GetAccountSummaryOutput{
			SummaryMap: map[string]int32{
				"Users":  3,
				"Groups": 1,
			},
		},
	}

	got := gatherAccountInfo(context.Background(), "eu-west-2", stsClient, iamClient)

	assert.Equal(t, "123456789012", got["AccountId"])
	assert.Equal(t, "arn:aws:iam::123456789012:user/alice", got["Arn"])
	assert.Equal(t, "AIDA1234567EXAMPLE", got["UserId"])
	assert.Equal(t, "eu-west-2", got["Region"])
	assert.Equal(t, []string{"my-org"}, got["AccountAliases"])
	assert.Equal(t, map[string]int32{"Users": 3, "Groups": 1}, got["AccountSummary"])
}

// TestGatherAccountInfo_STSFailureLogsAndContinues — sts identity fails
// but the panel still gets Region + IAM data. The user sees a partial
// account info card rather than a 500.
func TestGatherAccountInfo_STSFailureLogsAndContinues(t *testing.T) {
	t.Parallel()
	stsClient := &fakeSTSClient{err: errors.New("sts down")}
	iamClient := &fakeIAMAccountClient{
		aliasesOut: &iam.ListAccountAliasesOutput{
			AccountAliases: []string{"my-org"},
		},
		summaryOut: &iam.GetAccountSummaryOutput{
			SummaryMap: map[string]int32{"Users": 7},
		},
	}

	got := gatherAccountInfo(context.Background(), "us-east-1", stsClient, iamClient)

	_, hasAccountID := got["AccountId"]
	assert.False(t, hasAccountID, "AccountId must be omitted when GetCallerIdentity fails")
	_, hasArn := got["Arn"]
	assert.False(t, hasArn, "Arn must be omitted when GetCallerIdentity fails")
	_, hasUserID := got["UserId"]
	assert.False(t, hasUserID, "UserId must be omitted when GetCallerIdentity fails")

	assert.Equal(t, "us-east-1", got["Region"], "Region must be set even when sts fails")
	assert.Equal(t, []string{"my-org"}, got["AccountAliases"])
	assert.Equal(t, map[string]int32{"Users": 7}, got["AccountSummary"])
}

// TestGatherAccountInfo_IAMAliasesFailureLogsAndContinues — IAM aliases
// failing must not prevent caller identity + summary from rendering.
func TestGatherAccountInfo_IAMAliasesFailureLogsAndContinues(t *testing.T) {
	t.Parallel()
	stsClient := &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{
			Account: aws.String("123456789012"),
			Arn:     aws.String("arn:aws:iam::123456789012:user/bob"),
			UserId:  aws.String("AIDABOB"),
		},
	}
	iamClient := &fakeIAMAccountClient{
		aliasesErr: errors.New("AccessDenied"),
		summaryOut: &iam.GetAccountSummaryOutput{
			SummaryMap: map[string]int32{"Users": 1},
		},
	}

	got := gatherAccountInfo(context.Background(), "ap-southeast-1", stsClient, iamClient)

	assert.Equal(t, "123456789012", got["AccountId"])
	_, hasAliases := got["AccountAliases"]
	assert.False(t, hasAliases, "AccountAliases must be omitted when ListAccountAliases fails")
	assert.Equal(t, map[string]int32{"Users": 1}, got["AccountSummary"])
}

// TestGatherAccountInfo_IAMSummaryFailureLogsAndContinues — summary
// failing alone should not blank out the rest.
func TestGatherAccountInfo_IAMSummaryFailureLogsAndContinues(t *testing.T) {
	t.Parallel()
	stsClient := &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{
			Account: aws.String("999999999999"),
			Arn:     aws.String("arn:aws:iam::999999999999:user/svc"),
			UserId:  aws.String("AIDASVC"),
		},
	}
	iamClient := &fakeIAMAccountClient{
		aliasesOut: &iam.ListAccountAliasesOutput{AccountAliases: []string{"prod"}},
		summaryErr: errors.New("Throttling"),
	}

	got := gatherAccountInfo(context.Background(), "eu-central-1", stsClient, iamClient)

	assert.Equal(t, "999999999999", got["AccountId"])
	assert.Equal(t, []string{"prod"}, got["AccountAliases"])
	_, hasSummary := got["AccountSummary"]
	assert.False(t, hasSummary, "AccountSummary must be omitted when GetAccountSummary fails")
}

// TestGatherAccountInfo_EmptyAliasesNotSet mirrors the InsideOut backend's
// behaviour: ListAccountAliases returning successfully but empty must
// NOT add an "AccountAliases":[] key — keeps the panel JSON tight.
func TestGatherAccountInfo_EmptyAliasesNotSet(t *testing.T) {
	t.Parallel()
	stsClient := &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{
			Account: aws.String("123456789012"),
			Arn:     aws.String("arn:aws:iam::123456789012:root"),
			UserId:  aws.String("123456789012"),
		},
	}
	iamClient := &fakeIAMAccountClient{
		aliasesOut: &iam.ListAccountAliasesOutput{AccountAliases: []string{}},
	}

	got := gatherAccountInfo(context.Background(), "us-east-2", stsClient, iamClient)

	_, hasAliases := got["AccountAliases"]
	assert.False(t, hasAliases, "empty AccountAliases must be omitted from the panel payload")
}

// TestInspectAccount_UnknownAction returns the standard
// unsupported-action error so callers get a structured failure.
func TestInspectAccount_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectAccount(context.Background(), aws.Config{Region: "us-east-1"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account")
	assert.Contains(t, err.Error(), "no-such-action")
}
