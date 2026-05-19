// AWS App Runner inspector tests (issue #622).
//
// Pins the #255 contract: empty list-services response MUST marshal as
// JSON `[]`, never `null`. Also pins describe-service's required
// service_arn surface and the metrics-routing arm.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	apprunnertypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAppRunnerClient struct {
	listOut     *apprunner.ListServicesOutput
	describeOut *apprunner.DescribeServiceOutput
	describeIn  *apprunner.DescribeServiceInput
	err         error
}

func (f *fakeAppRunnerClient) ListServices(_ context.Context, _ *apprunner.ListServicesInput, _ ...func(*apprunner.Options)) (*apprunner.ListServicesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.listOut == nil {
		return &apprunner.ListServicesOutput{}, nil
	}
	return f.listOut, nil
}

func (f *fakeAppRunnerClient) DescribeService(_ context.Context, in *apprunner.DescribeServiceInput, _ ...func(*apprunner.Options)) (*apprunner.DescribeServiceOutput, error) {
	f.describeIn = in
	if f.err != nil {
		return nil, f.err
	}
	if f.describeOut == nil {
		return &apprunner.DescribeServiceOutput{}, nil
	}
	return f.describeOut, nil
}

// TestListAppRunnerServices_EmptyResult — #255 contract: empty response
// is JSON `[]`, not `null`. The reliable UI's AppRunner panel gates
// render on the list shape.
func TestListAppRunnerServices_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listAppRunnerServices(context.Background(), &fakeAppRunnerClient{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty service list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

// TestListAppRunnerServices_ExplicitEmptySliceNormalized — separate
// code path from typed-nil: when the AWS SDK returns an explicitly-
// empty slice, it must still pass through as a non-nil []. Pins the
// #255 contract against a future SDK behavior change.
func TestListAppRunnerServices_ExplicitEmptySliceNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeAppRunnerClient{listOut: &apprunner.ListServicesOutput{
		ServiceSummaryList: []apprunnertypes.ServiceSummary{}, // explicitly empty
	}}
	got, err := listAppRunnerServices(context.Background(), client)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListAppRunnerServices_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeAppRunnerClient{
		listOut: &apprunner.ListServicesOutput{
			ServiceSummaryList: []apprunnertypes.ServiceSummary{
				{ServiceArn: aws.String("arn:aws:apprunner:us-east-1:1:service/abc"), ServiceName: aws.String("demo")},
			},
		},
	}
	got, err := listAppRunnerServices(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "demo", aws.ToString(got[0].ServiceName))
}

func TestListAppRunnerServices_APIError(t *testing.T) {
	t.Parallel()
	_, err := listAppRunnerServices(context.Background(), &fakeAppRunnerClient{err: errors.New("AccessDenied")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

func TestDescribeAppRunnerService_PassesARN(t *testing.T) {
	t.Parallel()
	client := &fakeAppRunnerClient{}
	_, err := describeAppRunnerService(context.Background(), client, "arn:aws:apprunner:us-east-1:1:service/xyz")
	require.NoError(t, err)
	require.NotNil(t, client.describeIn)
	assert.Equal(t, "arn:aws:apprunner:us-east-1:1:service/xyz", aws.ToString(client.describeIn.ServiceArn))
}

func TestAppRunnerFilterServiceArn_RequiresARN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing key", `{"project":"demo"}`},
		{"empty value", `{"service_arn":""}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := appRunnerFilterServiceArn(tc.filters)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "service_arn")
		})
	}
}

func TestAppRunnerFilterServiceArn_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := appRunnerFilterServiceArn(`{not json}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid filters JSON")
}

func TestAppRunnerFilterServiceArn_Valid(t *testing.T) {
	t.Parallel()
	arn, err := appRunnerFilterServiceArn(`{"service_arn":"arn:aws:apprunner:us-east-1:1:service/abc"}`)
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:apprunner:us-east-1:1:service/abc", arn)
}

// TestInspectAppRunner_GetMetricsRoutesToMetricsPackage — get-metrics
// short-circuits to the metrics-package sentinel so callers know to
// invoke pkg/observability/metrics for the AWS/AppRunner series.
func TestInspectAppRunner_GetMetricsRoutesToMetricsPackage(t *testing.T) {
	t.Parallel()
	_, err := inspectAppRunner(context.Background(), aws.Config{Region: "us-east-1"}, "get-metrics", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUseMetricsPackage)
	assert.Contains(t, err.Error(), "apprunner")
}

func TestInspectAppRunner_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectAppRunner(context.Background(), aws.Config{Region: "us-east-1"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apprunner")
	assert.Contains(t, err.Error(), "no-such-action")
}
