// ACM inspector tests (issue #596).
//
// Pins the #255 contract: empty list-certificates response MUST marshal
// as JSON `[]`, never `null`. Also pins describe-certificate's required
// certificate_arn surface and the metrics-routing arm.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeACMClient struct {
	listOut          *acm.ListCertificatesOutput
	describeOut      *acm.DescribeCertificateOutput
	describeIn       *acm.DescribeCertificateInput
	err              error
}

func (f *fakeACMClient) ListCertificates(_ context.Context, _ *acm.ListCertificatesInput, _ ...func(*acm.Options)) (*acm.ListCertificatesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.listOut == nil {
		return &acm.ListCertificatesOutput{}, nil
	}
	return f.listOut, nil
}

func (f *fakeACMClient) DescribeCertificate(_ context.Context, in *acm.DescribeCertificateInput, _ ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error) {
	f.describeIn = in
	if f.err != nil {
		return nil, f.err
	}
	if f.describeOut == nil {
		return &acm.DescribeCertificateOutput{}, nil
	}
	return f.describeOut, nil
}

// TestListCertificates_EmptyResult — #255 contract: empty response is
// JSON `[]`, not `null`. The reliable UI's ACM panel gates render on the
// list shape.
func TestListCertificates_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listCertificates(context.Background(), &fakeACMClient{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty cert list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListCertificates_TypedNilSliceNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeACMClient{listOut: &acm.ListCertificatesOutput{}}
	got, err := listCertificates(context.Background(), client)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, _ := json.Marshal(got)
	assert.Equal(t, "[]", string(b))
}

func TestListCertificates_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeACMClient{
		listOut: &acm.ListCertificatesOutput{
			CertificateSummaryList: []acmtypes.CertificateSummary{
				{CertificateArn: aws.String("arn:aws:acm:us-east-1:1:certificate/abc"), DomainName: aws.String("example.com")},
			},
		},
	}
	got, err := listCertificates(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "example.com", aws.ToString(got[0].DomainName))
}

func TestListCertificates_APIError(t *testing.T) {
	t.Parallel()
	_, err := listCertificates(context.Background(), &fakeACMClient{err: errors.New("AccessDenied")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

func TestDescribeCertificate_PassesARN(t *testing.T) {
	t.Parallel()
	client := &fakeACMClient{}
	_, err := describeCertificate(context.Background(), client, "arn:aws:acm:us-east-1:1:certificate/xyz")
	require.NoError(t, err)
	require.NotNil(t, client.describeIn)
	assert.Equal(t, "arn:aws:acm:us-east-1:1:certificate/xyz", aws.ToString(client.describeIn.CertificateArn))
}

func TestACMFilterCertificateArn_RequiresARN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing key", `{"project":"demo"}`},
		{"empty value", `{"certificate_arn":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := acmFilterCertificateArn(tc.filters)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "certificate_arn")
		})
	}
}

func TestACMFilterCertificateArn_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := acmFilterCertificateArn(`{not json}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid filters JSON")
}

func TestACMFilterCertificateArn_Valid(t *testing.T) {
	t.Parallel()
	arn, err := acmFilterCertificateArn(`{"certificate_arn":"arn:aws:acm:us-east-1:1:certificate/abc"}`)
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:acm:us-east-1:1:certificate/abc", arn)
}

// TestInspectACM_GetMetricsRoutesToMetricsPackage — get-metrics short-
// circuits to the metrics-package sentinel so callers know to invoke
// pkg/observability/metrics for the DaysToExpiry series.
func TestInspectACM_GetMetricsRoutesToMetricsPackage(t *testing.T) {
	t.Parallel()
	_, err := inspectACM(context.Background(), aws.Config{Region: "us-east-1"}, "get-metrics", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUseMetricsPackage)
	assert.Contains(t, err.Error(), "acm")
}

func TestInspectACM_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectACM(context.Background(), aws.Config{Region: "us-east-1"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acm")
	assert.Contains(t, err.Error(), "no-such-action")
}
