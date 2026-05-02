// Cognito inspector tests. The interesting bit is the three-step pivot:
// ListUserPools (no ARN) → DescribeUserPool (ARN) → ListTagsForResource.
// Per-pool error handling at each step is log+skip (fail-closed).

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCognitoClient struct {
	listOut *cognitoidentityprovider.ListUserPoolsOutput
	listErr error
	descOut *cognitoidentityprovider.DescribeUserPoolOutput
	descErr error
	tagsOut *cognitoidentityprovider.ListTagsForResourceOutput
	tagsErr error
}

func (f *fakeCognitoClient) ListUserPools(_ context.Context, _ *cognitoidentityprovider.ListUserPoolsInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListUserPoolsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut == nil {
		return &cognitoidentityprovider.ListUserPoolsOutput{}, nil
	}
	return f.listOut, nil
}

func (f *fakeCognitoClient) DescribeUserPool(_ context.Context, _ *cognitoidentityprovider.DescribeUserPoolInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolOutput, error) {
	if f.descErr != nil {
		return nil, f.descErr
	}
	if f.descOut == nil {
		return &cognitoidentityprovider.DescribeUserPoolOutput{}, nil
	}
	return f.descOut, nil
}

func (f *fakeCognitoClient) ListTagsForResource(_ context.Context, _ *cognitoidentityprovider.ListTagsForResourceInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListTagsForResourceOutput, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &cognitoidentityprovider.ListTagsForResourceOutput{}, nil
	}
	return f.tagsOut, nil
}

func TestFilterCognitoUserPoolsByProjectTag_EmptyProjectShortCircuits(t *testing.T) {
	t.Parallel()
	client := &fakeCognitoClient{
		listOut: &cognitoidentityprovider.ListUserPoolsOutput{
			UserPools: []cognitoidptypes.UserPoolDescriptionType{
				{Id: aws.String("p1")},
			},
		},
	}
	got, err := filterCognitoUserPoolsByProjectTag(context.Background(), client, "")
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func TestFilterCognitoUserPoolsByProjectTag_Match(t *testing.T) {
	t.Parallel()
	client := &fakeCognitoClient{
		listOut: &cognitoidentityprovider.ListUserPoolsOutput{
			UserPools: []cognitoidptypes.UserPoolDescriptionType{
				{Id: aws.String("p1")},
			},
		},
		descOut: &cognitoidentityprovider.DescribeUserPoolOutput{
			UserPool: &cognitoidptypes.UserPoolType{Arn: aws.String("arn:p1")},
		},
		tagsOut: &cognitoidentityprovider.ListTagsForResourceOutput{
			Tags: map[string]string{"Project": "my-stack"},
		},
	}
	got, err := filterCognitoUserPoolsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestFilterCognitoUserPoolsByProjectTag_DescribeErrorSkips(t *testing.T) {
	t.Parallel()
	// Concurrent delete or TooManyRequestsException → log+skip.
	client := &fakeCognitoClient{
		listOut: &cognitoidentityprovider.ListUserPoolsOutput{
			UserPools: []cognitoidptypes.UserPoolDescriptionType{
				{Id: aws.String("p1")},
			},
		},
		descErr: errors.New("denied"),
	}
	got, err := filterCognitoUserPoolsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFilterCognitoUserPoolsByProjectTag_TagsErrorSkips(t *testing.T) {
	t.Parallel()
	client := &fakeCognitoClient{
		listOut: &cognitoidentityprovider.ListUserPoolsOutput{
			UserPools: []cognitoidptypes.UserPoolDescriptionType{
				{Id: aws.String("p1")},
			},
		},
		descOut: &cognitoidentityprovider.DescribeUserPoolOutput{
			UserPool: &cognitoidptypes.UserPoolType{Arn: aws.String("arn:p1")},
		},
		tagsErr: errors.New("throttle"),
	}
	got, err := filterCognitoUserPoolsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFilterCognitoUserPoolsByProjectTag_ListErrorAborts(t *testing.T) {
	t.Parallel()
	client := &fakeCognitoClient{listErr: errors.New("denied")}
	_, err := filterCognitoUserPoolsByProjectTag(context.Background(), client, "my-stack")
	require.Error(t, err)
}
