// AWS SageMaker inspector tests (issue #622).
//
// Pins the #255 contract: empty list-domains / list-user-profiles /
// list-endpoints responses MUST marshal as JSON `[]`, never `null`.
// Also pins describe-domain's required domain_id surface and the
// metrics-routing arm. list-endpoints (#797) is the account-wide
// EndpointName discovery action for the AWS/SageMaker metrics namespace.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	sagemakertypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSageMakerClient struct {
	listDomainsOut      *sagemaker.ListDomainsOutput
	describeDomainOut   *sagemaker.DescribeDomainOutput
	describeDomainIn    *sagemaker.DescribeDomainInput
	listUserProfilesOut *sagemaker.ListUserProfilesOutput
	listEndpointsOut    *sagemaker.ListEndpointsOutput
	err                 error
}

func (f *fakeSageMakerClient) ListDomains(_ context.Context, _ *sagemaker.ListDomainsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListDomainsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.listDomainsOut == nil {
		return &sagemaker.ListDomainsOutput{}, nil
	}
	return f.listDomainsOut, nil
}

func (f *fakeSageMakerClient) DescribeDomain(_ context.Context, in *sagemaker.DescribeDomainInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeDomainOutput, error) {
	f.describeDomainIn = in
	if f.err != nil {
		return nil, f.err
	}
	if f.describeDomainOut == nil {
		return &sagemaker.DescribeDomainOutput{}, nil
	}
	return f.describeDomainOut, nil
}

func (f *fakeSageMakerClient) ListUserProfiles(_ context.Context, _ *sagemaker.ListUserProfilesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListUserProfilesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.listUserProfilesOut == nil {
		return &sagemaker.ListUserProfilesOutput{}, nil
	}
	return f.listUserProfilesOut, nil
}

func (f *fakeSageMakerClient) ListEndpoints(_ context.Context, _ *sagemaker.ListEndpointsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListEndpointsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.listEndpointsOut == nil {
		return &sagemaker.ListEndpointsOutput{}, nil
	}
	return f.listEndpointsOut, nil
}

// TestListSageMakerDomains_EmptyResult — #255 contract: empty response
// is JSON `[]`, not `null`.
func TestListSageMakerDomains_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listSageMakerDomains(context.Background(), &fakeSageMakerClient{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty domain list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListSageMakerDomains_ExplicitEmptySliceNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeSageMakerClient{listDomainsOut: &sagemaker.ListDomainsOutput{
		Domains: []sagemakertypes.DomainDetails{}, // explicitly empty
	}}
	got, err := listSageMakerDomains(context.Background(), client)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListSageMakerDomains_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeSageMakerClient{
		listDomainsOut: &sagemaker.ListDomainsOutput{
			Domains: []sagemakertypes.DomainDetails{
				{DomainId: aws.String("d-abc"), DomainName: aws.String("studio-1")},
			},
		},
	}
	got, err := listSageMakerDomains(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "studio-1", aws.ToString(got[0].DomainName))
}

func TestListSageMakerDomains_APIError(t *testing.T) {
	t.Parallel()
	_, err := listSageMakerDomains(context.Background(), &fakeSageMakerClient{err: errors.New("AccessDenied")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

func TestListSageMakerUserProfiles_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listSageMakerUserProfiles(context.Background(), &fakeSageMakerClient{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty user-profile list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListSageMakerUserProfiles_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeSageMakerClient{
		listUserProfilesOut: &sagemaker.ListUserProfilesOutput{
			UserProfiles: []sagemakertypes.UserProfileDetails{
				{UserProfileName: aws.String("alice")},
			},
		},
	}
	got, err := listSageMakerUserProfiles(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "alice", aws.ToString(got[0].UserProfileName))
}

// TestListSageMakerEndpoints_EmptyResult — #255 contract (#797): an
// empty endpoint list must marshal as JSON `[]`, never `null`, so the
// downstream account-wide EndpointName discovery surface renders.
func TestListSageMakerEndpoints_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listSageMakerEndpoints(context.Background(), &fakeSageMakerClient{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty endpoint list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListSageMakerEndpoints_ExplicitEmptySliceNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeSageMakerClient{listEndpointsOut: &sagemaker.ListEndpointsOutput{
		Endpoints: []sagemakertypes.EndpointSummary{}, // explicitly empty
	}}
	got, err := listSageMakerEndpoints(context.Background(), client)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListSageMakerEndpoints_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeSageMakerClient{
		listEndpointsOut: &sagemaker.ListEndpointsOutput{
			Endpoints: []sagemakertypes.EndpointSummary{
				{EndpointName: aws.String("my-endpoint")},
			},
		},
	}
	got, err := listSageMakerEndpoints(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "my-endpoint", aws.ToString(got[0].EndpointName))
}

func TestListSageMakerEndpoints_APIError(t *testing.T) {
	t.Parallel()
	_, err := listSageMakerEndpoints(context.Background(), &fakeSageMakerClient{err: errors.New("AccessDenied")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

func TestDescribeSageMakerDomain_PassesID(t *testing.T) {
	t.Parallel()
	client := &fakeSageMakerClient{}
	_, err := describeSageMakerDomain(context.Background(), client, "d-xyz")
	require.NoError(t, err)
	require.NotNil(t, client.describeDomainIn)
	assert.Equal(t, "d-xyz", aws.ToString(client.describeDomainIn.DomainId))
}

func TestSageMakerFilterDomainID_RequiresID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing key", `{"project":"demo"}`},
		{"empty value", `{"domain_id":""}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := sageMakerFilterDomainID(tc.filters)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "domain_id")
		})
	}
}

func TestSageMakerFilterDomainID_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := sageMakerFilterDomainID(`{not json}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid filters JSON")
}

func TestSageMakerFilterDomainID_Valid(t *testing.T) {
	t.Parallel()
	id, err := sageMakerFilterDomainID(`{"domain_id":"d-xyz"}`)
	require.NoError(t, err)
	assert.Equal(t, "d-xyz", id)
}

// TestInspectSageMaker_GetMetricsRoutesToMetricsPackage — get-metrics
// short-circuits to the metrics-package sentinel.
func TestInspectSageMaker_GetMetricsRoutesToMetricsPackage(t *testing.T) {
	t.Parallel()
	_, err := inspectSageMaker(context.Background(), aws.Config{Region: "us-east-1"}, "get-metrics", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUseMetricsPackage)
	assert.Contains(t, err.Error(), "sagemaker")
}

func TestInspectSageMaker_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectSageMaker(context.Background(), aws.Config{Region: "us-east-1"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sagemaker")
	assert.Contains(t, err.Error(), "no-such-action")
}

// TestInspectSageMaker_ListEndpointsRoutesToHandler — #797: the
// list-endpoints arm must dispatch to the handler, not fall through to
// unsupportedActionError. With an empty config the SDK call fails before
// reaching AWS, so the contract we pin is "did NOT bounce off the switch
// as an unsupported action" (mirrors the dispatcher drift gate). This
// closes the gap that firstAction("sagemaker") leaves: it returns
// list-domains, so TestInspectCoversAllAWSServices never exercises this
// arm.
func TestInspectSageMaker_ListEndpointsRoutesToHandler(t *testing.T) {
	t.Parallel()
	_, err := inspectSageMaker(context.Background(), aws.Config{Region: "us-east-1"}, "list-endpoints", "")
	if err != nil {
		assert.NotContains(t, err.Error(), "no-such-action")
		assert.NotContains(t, err.Error(), "unsupported",
			"list-endpoints must route to its handler, got unsupported-action error: %v", err)
	}
}
