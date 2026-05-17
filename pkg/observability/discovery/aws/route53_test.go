// Route 53 inspector tests (issue #596).
//
// Pins the #255 contract end-to-end: empty list-hosted-zones and
// list-resource-record-sets responses MUST marshal as JSON `[]`, never
// `null`. Also pins the filters-envelope error path on
// list-resource-record-sets (the API requires a hosted_zone_id and
// silently calling without one returns a less actionable
// ValidationException at the SDK layer).

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRoute53Client struct {
	listHostedZonesOut       *route53.ListHostedZonesOutput
	listResourceRecordSetsIn *route53.ListResourceRecordSetsInput
	listResourceRecordSetsOut *route53.ListResourceRecordSetsOutput
	err                      error
}

func (f *fakeRoute53Client) ListHostedZones(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.listHostedZonesOut == nil {
		return &route53.ListHostedZonesOutput{}, nil
	}
	return f.listHostedZonesOut, nil
}

func (f *fakeRoute53Client) ListResourceRecordSets(_ context.Context, in *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	f.listResourceRecordSetsIn = in
	if f.err != nil {
		return nil, f.err
	}
	if f.listResourceRecordSetsOut == nil {
		return &route53.ListResourceRecordSetsOutput{}, nil
	}
	return f.listResourceRecordSetsOut, nil
}

// TestListHostedZones_EmptyResult — empty AWS response marshals as JSON
// `[]`, not `null` (#255). The reliable UI gates panel render on the
// array shape; `null` collapses through every empty-state branch.
func TestListHostedZones_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listHostedZones(context.Background(), &fakeRoute53Client{})
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty result must be a non-nil slice")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b), "#255: empty JSON wire must be `[]`, not `null`")
}

// TestListHostedZones_TypedNilSliceNormalized — when the AWS SDK
// populates HostedZones as a typed-nil slice (the SDK's empty-response
// behavior), nilSliceToEmpty must normalize it to []. Pattern B from
// the CONTRIBUTING.md cheat-sheet.
func TestListHostedZones_TypedNilSliceNormalized(t *testing.T) {
	t.Parallel()
	client := &fakeRoute53Client{
		// HostedZones is the zero value — a typed-nil []HostedZone.
		listHostedZonesOut: &route53.ListHostedZonesOutput{},
	}
	got, err := listHostedZones(context.Background(), client)
	require.NoError(t, err)
	require.NotNil(t, got, "typed-nil from SDK must be normalized to []")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListHostedZones_NonEmpty(t *testing.T) {
	t.Parallel()
	client := &fakeRoute53Client{
		listHostedZonesOut: &route53.ListHostedZonesOutput{
			HostedZones: []route53types.HostedZone{
				{Id: aws.String("/hostedzone/Z111"), Name: aws.String("example.com.")},
				{Id: aws.String("/hostedzone/Z222"), Name: aws.String("other.com.")},
			},
		},
	}
	got, err := listHostedZones(context.Background(), client)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "/hostedzone/Z111", aws.ToString(got[0].Id))
}

func TestListHostedZones_APIError(t *testing.T) {
	t.Parallel()
	client := &fakeRoute53Client{err: errors.New("AccessDenied")}
	_, err := listHostedZones(context.Background(), client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AccessDenied")
}

// TestListResourceRecordSets_EmptyResult — same #255 contract on the
// record-set list. Wired into the live probe at
// live_probe_255_test.go (filtered + unfiltered variants).
func TestListResourceRecordSets_EmptyResult(t *testing.T) {
	t.Parallel()
	got, err := listResourceRecordSets(context.Background(), &fakeRoute53Client{}, "Z111")
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty record-set list must be non-nil")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestListResourceRecordSets_PassesZoneID(t *testing.T) {
	t.Parallel()
	client := &fakeRoute53Client{}
	_, err := listResourceRecordSets(context.Background(), client, "Z222")
	require.NoError(t, err)
	require.NotNil(t, client.listResourceRecordSetsIn)
	assert.Equal(t, "Z222", aws.ToString(client.listResourceRecordSetsIn.HostedZoneId))
}

// TestRoute53FilterZoneID_RequiresHostedZoneID — the API mandates a
// HostedZoneId. The inspector surfaces a clear structured error rather
// than letting the SDK emit a ValidationException with no panel
// guidance.
func TestRoute53FilterZoneID_RequiresHostedZoneID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing key", `{"project":"demo"}`},
		{"empty value", `{"hosted_zone_id":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := route53FilterZoneID(tc.filters)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "hosted_zone_id")
		})
	}
}

func TestRoute53FilterZoneID_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := route53FilterZoneID(`{not json}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid filters JSON")
}

func TestRoute53FilterZoneID_ValidFilters(t *testing.T) {
	t.Parallel()
	id, err := route53FilterZoneID(`{"hosted_zone_id":"Z2FDTNDATAQYW2"}`)
	require.NoError(t, err)
	assert.Equal(t, "Z2FDTNDATAQYW2", id)
}

// TestInspectRoute53_UnknownAction — bogus actions surface the canonical
// unsupported-action message with the supported list embedded.
func TestInspectRoute53_UnknownAction(t *testing.T) {
	t.Parallel()
	_, err := inspectRoute53(context.Background(), aws.Config{Region: "us-east-1"}, "no-such-action", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "route53")
	assert.Contains(t, err.Error(), "no-such-action")
}
