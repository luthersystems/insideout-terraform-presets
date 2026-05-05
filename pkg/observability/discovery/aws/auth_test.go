// Cognito inspector tests. The interesting bit is the three-step pivot:
// ListUserPools (no ARN) → DescribeUserPool (ARN) → ListTagsForResource.
// Per-pool error handling at each step is log+skip (fail-closed).

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCognitoClient supports both a single canned response (for
// short-circuit and error-path tests) and per-pool routing
// (descByPoolID + tagsByARN) so the match-direction test can return
// distinct tag sets for distinct pools and verify the filter actually
// selects on Project.
type fakeCognitoClient struct {
	listOut      *cognitoidentityprovider.ListUserPoolsOutput
	listErr      error
	descOut      *cognitoidentityprovider.DescribeUserPoolOutput
	descErr      error
	tagsOut      *cognitoidentityprovider.ListTagsForResourceOutput
	tagsErr      error
	descByPoolID map[string]*cognitoidentityprovider.DescribeUserPoolOutput
	tagsByARN    map[string]*cognitoidentityprovider.ListTagsForResourceOutput
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

func (f *fakeCognitoClient) DescribeUserPool(_ context.Context, in *cognitoidentityprovider.DescribeUserPoolInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.DescribeUserPoolOutput, error) {
	if f.descErr != nil {
		return nil, f.descErr
	}
	if f.descByPoolID != nil {
		if out, ok := f.descByPoolID[aws.ToString(in.UserPoolId)]; ok {
			return out, nil
		}
		return &cognitoidentityprovider.DescribeUserPoolOutput{}, nil
	}
	if f.descOut == nil {
		return &cognitoidentityprovider.DescribeUserPoolOutput{}, nil
	}
	return f.descOut, nil
}

func (f *fakeCognitoClient) ListTagsForResource(_ context.Context, in *cognitoidentityprovider.ListTagsForResourceInput, _ ...func(*cognitoidentityprovider.Options)) (*cognitoidentityprovider.ListTagsForResourceOutput, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsByARN != nil {
		if out, ok := f.tagsByARN[aws.ToString(in.ResourceArn)]; ok {
			return out, nil
		}
		return &cognitoidentityprovider.ListTagsForResourceOutput{}, nil
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
	// Two pools: p1 tagged Project=my-stack (matches), p2 tagged
	// Project=other (does not). Per-pool routing means a no-op filter
	// would return both — the assert below would fail, catching the
	// regression.
	client := &fakeCognitoClient{
		listOut: &cognitoidentityprovider.ListUserPoolsOutput{
			UserPools: []cognitoidptypes.UserPoolDescriptionType{
				{Id: aws.String("p1")},
				{Id: aws.String("p2")},
			},
		},
		descByPoolID: map[string]*cognitoidentityprovider.DescribeUserPoolOutput{
			"p1": {UserPool: &cognitoidptypes.UserPoolType{Arn: aws.String("arn:p1")}},
			"p2": {UserPool: &cognitoidptypes.UserPoolType{Arn: aws.String("arn:p2")}},
		},
		tagsByARN: map[string]*cognitoidentityprovider.ListTagsForResourceOutput{
			"arn:p1": {Tags: map[string]string{"Project": "my-stack"}},
			"arn:p2": {Tags: map[string]string{"Project": "other"}},
		},
	}
	got, err := filterCognitoUserPoolsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "p1", aws.ToString(got[0].Id))
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

func TestFilterCognitoUserPoolsByProjectTag_NoPools_EmptySlice(t *testing.T) {
	t.Parallel()
	client := &fakeCognitoClient{listOut: &cognitoidentityprovider.ListUserPoolsOutput{}}
	got, err := filterCognitoUserPoolsByProjectTag(context.Background(), client, "any-project")
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Cognito list-user-pools must marshal as [] not null (#256)")
}
